package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/devblac/prw/internal/config"
)

// TestMain points the config file at a throwaway directory so that a test which
// forgets to override config.ConfigPath cannot overwrite the real ~/.prw/config.json.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "prw-cmd-test-")
	if err != nil {
		panic(err)
	}
	config.ConfigPath = func() (string, error) {
		return filepath.Join(dir, ".prw", "config.json"), nil
	}
	// Never pick up the developer's (or CI runner's) gh login or GH_TOKEN.
	config.GHAuthToken = func() (string, error) { return "", errors.New("gh disabled in tests") }
	os.Unsetenv("GH_TOKEN")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
