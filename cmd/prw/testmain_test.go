package main

import (
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
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
