package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain points every directory this tool could touch at a scratch home, so
// no test reaches the live config or state: HOME alone is not enough, since
// os.UserConfigDir prefers XDG_CONFIG_HOME, and an unisolated migration test
// once moved a live config dir. Tests that need other dirs set their own.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "flatcircle-test-home")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Setenv("HOME", home)
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for _, suffix := range []string{"CONFIG_DIR", "STATE_DIR"} {
		for _, k := range envNames(suffix) {
			os.Unsetenv(k)
		}
	}
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
