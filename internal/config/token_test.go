package config

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func stubGHAuthToken(t *testing.T, token string, err error) {
	t.Helper()
	old := GHAuthToken
	GHAuthToken = func() (string, error) { return token, err }
	t.Cleanup(func() { GHAuthToken = old })
}

func TestResolveToken_Precedence(t *testing.T) {
	tests := []struct {
		name        string
		configToken string
		githubToken string
		ghToken     string
		cliToken    string
		cliErr      error
		wantToken   string
		wantSource  string
	}{
		{"config wins over everything", "cfg", "env", "gh-env", "cli", nil, "cfg", TokenSourceConfig},
		{"GITHUB_TOKEN next", "", "env", "gh-env", "cli", nil, "env", TokenSourceEnv},
		{"GH_TOKEN next", "", "", "gh-env", "cli", nil, "gh-env", TokenSourceGHEnv},
		{"gh auth token last", "", "", "", "cli", nil, "cli", TokenSourceGitHubCLI},
		{"gh not logged in", "", "", "", "", errors.New("not logged in"), "", TokenSourceNone},
		{"gh returns empty output", "", "", "", "", nil, "", TokenSourceNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GITHUB_TOKEN", tt.githubToken)
			t.Setenv("GH_TOKEN", tt.ghToken)
			stubGHAuthToken(t, tt.cliToken, tt.cliErr)

			cfg := &Config{GitHubToken: tt.configToken}
			token, source := cfg.ResolveToken()
			if token != tt.wantToken || source != tt.wantSource {
				t.Errorf("ResolveToken() = (%q, %q), want (%q, %q)", token, source, tt.wantToken, tt.wantSource)
			}
			if got := cfg.GetToken(); got != tt.wantToken {
				t.Errorf("GetToken() = %q, want %q", got, tt.wantToken)
			}
		})
	}
}

func TestResolveToken_DoesNotRunGHWhenTokenAlreadyFound(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "env")
	called := false
	old := GHAuthToken
	GHAuthToken = func() (string, error) { called = true; return "cli", nil }
	defer func() { GHAuthToken = old }()

	(&Config{}).ResolveToken()
	if called {
		t.Error("gh must not be executed when a token is already configured")
	}
}

// installFakeGH copies this test binary into a temp dir as `gh` and puts only that dir
// on PATH. TestMain turns the copy into a fake gh when envFakeGH is set.
func installFakeGH(t *testing.T) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Skipf("cannot locate test binary: %v", err)
	}
	dir := t.TempDir()
	name := "gh"
	if runtime.GOOS == "windows" {
		name = "gh.exe"
	}
	src, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY, 0755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		t.Fatal(err)
	}
	dst.Close()

	t.Setenv("PATH", dir)
	t.Setenv(envFakeGH, "1")
}

func TestGHAuthToken_RunsGitHubCLI(t *testing.T) {
	installFakeGH(t)
	t.Setenv(envFakeGHOutput, "gho_fake_token")

	token, err := realGHAuthToken()
	if err != nil {
		t.Fatalf("GHAuthToken failed: %v", err)
	}
	if token != "gho_fake_token" {
		t.Errorf("token = %q, want gho_fake_token (trailing newline must be trimmed)", token)
	}
}

func TestGHAuthToken_NotLoggedIn(t *testing.T) {
	installFakeGH(t)
	t.Setenv(envFakeGHFail, "1")

	if token, err := realGHAuthToken(); err == nil {
		t.Errorf("expected an error when gh is not logged in, got token %q", token)
	}
}

func TestGHAuthToken_GHNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if token, err := realGHAuthToken(); err == nil {
		t.Errorf("expected an error when gh is not on PATH, got token %q", token)
	}
}
