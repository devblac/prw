package forge

import "testing"

func TestParsePRURL(t *testing.T) {
	tests := []struct {
		url  string
		want Ref
	}{
		{"https://github.com/owner/repo/pull/123", Ref{GitHub, "owner", "repo", 123}},
		{"github.com/owner/repo/pull/7/files", Ref{GitHub, "owner", "repo", 7}},
		{"https://bitbucket.org/acme/api/pull-requests/42", Ref{Bitbucket, "acme", "api", 42}},
		{"https://bitbucket.org/acme/api/pull-requests/42/diff", Ref{Bitbucket, "acme", "api", 42}},
		{"bitbucket.org/acme/web-app/pull-requests/1", Ref{Bitbucket, "acme", "web-app", 1}},
		{"https://GitHub.Acme.com/team/api/pull/9", Ref{"github.acme.com", "team", "api", 9}},
		{"https://ghe.local:8443/team/api/pull/9/checks", Ref{"ghe.local:8443", "team", "api", 9}},
		{"https://www.github.com/owner/repo/pull/1", Ref{GitHub, "owner", "repo", 1}},
	}
	for _, tt := range tests {
		got, err := ParsePRURL(tt.url)
		if err != nil || got != tt.want {
			t.Errorf("ParsePRURL(%q) = %+v, %v; want %+v", tt.url, got, err, tt.want)
		}
	}
	for _, bad := range []string{"", "https://gitlab.com/o/r/-/merge_requests/1", "https://bitbucket.org/acme/api/pull/42", "https://github.com/o/r/pull-requests/1", "https://evil.com/github.com/o/r/pull/1"} {
		if _, err := ParsePRURL(bad); err == nil {
			t.Errorf("ParsePRURL(%q) should fail", bad)
		}
	}
}

func TestPRURLAndHosts(t *testing.T) {
	if got := PRURL("", "o", "r", 1); got != "https://github.com/o/r/pull/1" {
		t.Errorf("empty host (old watch-list entries) must mean GitHub: %s", got)
	}
	if got := (Ref{Bitbucket, "acme", "api", 42}).URL(); got != "https://bitbucket.org/acme/api/pull-requests/42" {
		t.Errorf("bitbucket URL = %s", got)
	}
	if StorageHost(GitHub) != "" || StorageHost("Bitbucket.org") != Bitbucket || !IsBitbucket("BITBUCKET.ORG") || IsBitbucket("") {
		t.Error("host normalization")
	}
	if StorageHost("GitHub.Acme.com") != "github.acme.com" || !IsEnterprise("github.acme.com") || IsEnterprise("") || IsEnterprise(Bitbucket) {
		t.Error("enterprise host normalization")
	}
	if got := PRURL("github.acme.com", "team", "api", 9); got != "https://github.acme.com/team/api/pull/9" {
		t.Errorf("enterprise URL = %s", got)
	}
	for host, want := range map[string]string{"": "o/r#1", Bitbucket: "bitbucket:o/r#1", "github.acme.com": "github.acme.com:o/r#1"} {
		if got := Display(host, "o", "r", 1); got != want {
			t.Errorf("Display(%q) = %s, want %s", host, got, want)
		}
	}
	for host, want := range map[string]string{"": "GitHub", Bitbucket: "Bitbucket", "github.acme.com": "GitHub Enterprise"} {
		if got := ServiceName(host); got != want {
			t.Errorf("ServiceName(%q) = %s", host, got)
		}
	}
}
