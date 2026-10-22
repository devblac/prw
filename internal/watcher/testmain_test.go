package watcher

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devblac/prw/internal/config"
)

// TestMain points the config file at a throwaway directory. The watcher saves its
// config after every check, so without this the suite would overwrite the real
// ~/.prw/config.json of whoever runs `go test`.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "prw-watcher-test-")
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
