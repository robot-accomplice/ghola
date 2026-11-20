package main

import (
	"os"
	"testing"
)

// TestMain points the config folder at an empty one, so that the config
// file of the developer who runs the tests cannot change their results.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ghola-config")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CONFIG_HOME", dir) //nolint:errcheck
	os.Setenv("APPDATA", dir)         //nolint:errcheck
	code := m.Run()
	os.RemoveAll(dir) //nolint:errcheck
	os.Exit(code)
}
