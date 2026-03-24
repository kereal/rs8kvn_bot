package main

import (
	"strings"
	"testing"
)

func TestGetVersion(t *testing.T) {
	t.Run("returns non-empty string", func(t *testing.T) {
		v := getVersion()
		if v == "" {
			t.Error("getVersion() returned empty string")
		}
	})

	t.Run("returns string with correct prefix", func(t *testing.T) {
		v := getVersion()
		if !strings.HasPrefix(v, "rs8kvn_bot@") {
			t.Errorf("getVersion() = %s, want prefix rs8kvn_bot@", v)
		}
	})

	t.Run("handles dev version gracefully", func(t *testing.T) {
		// When version is "dev", should still return a valid string
		v := getVersion()
		if !strings.Contains(v, "rs8kvn_bot@") {
			t.Errorf("getVersion() should contain rs8kvn_bot@, got %s", v)
		}
	})
}

func TestGetVersion_CommitVariable(t *testing.T) {
	// Test that commit variable is accessible
	t.Run("commit variable is defined", func(t *testing.T) {
		if commit == "" {
			t.Log("commit is empty (expected in test environment)")
		}
	})
}

func TestGetVersion_BuildTimeVariable(t *testing.T) {
	// Test that buildTime variable is accessible
	t.Run("buildTime variable is defined", func(t *testing.T) {
		if buildTime == "" {
			t.Log("buildTime is empty (expected in test environment)")
		}
	})
}
