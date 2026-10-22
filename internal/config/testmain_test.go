package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// Environment variables that turn this test binary into a fake `gh` executable
// (see TestGHAuthToken_RunsGitHubCLI).
const (
	envFakeGH       = "PRW_TEST_FAKE_GH"
	envFakeGHOutput = "PRW_TEST_FAKE_GH_OUTPUT"
	envFakeGHFail   = "PRW_TEST_FAKE_GH_FAIL"
)

var realGHAuthToken = GHAuthToken

func TestMain(m *testing.M) {
	if os.Getenv(envFakeGH) == "1" {
		runFakeGH()
		return
	}

	// Never pick up the developer's real gh login or GH_TOKEN in unit tests.
	GHAuthToken = func() (string, error) { return "", errors.New("gh disabled in tests") }
	os.Unsetenv("GH_TOKEN")

	os.Exit(m.Run())
}

// runFakeGH emulates `gh auth token --hostname github.com`, rejecting any other args.
func runFakeGH() {
	args := strings.Join(os.Args[1:], " ")
	if args != "auth token --hostname github.com" {
		fmt.Fprintf(os.Stderr, "fake gh: unexpected args %q\n", args)
		os.Exit(2)
	}
	if os.Getenv(envFakeGHFail) == "1" {
		fmt.Fprintln(os.Stderr, "You are not logged into any GitHub hosts. To log in, run: gh auth login")
		os.Exit(1)
	}
	fmt.Println(os.Getenv(envFakeGHOutput))
	os.Exit(0)
}
