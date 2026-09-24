package app

import (
	"path/filepath"
	"testing"
)

func TestPrivateConfigPathIsOptionalButMustLoadWhenSet(t *testing.T) {
	t.Setenv("PRIVATE_CONFIG_PATH", "")
	_, privateNodes, err := loadConfig()
	if err != nil || privateNodes != nil {
		t.Fatalf("unset private config should disable injection: %v, %v", privateNodes, err)
	}

	t.Setenv("PRIVATE_CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
	_, _, err = loadConfig()
	if err == nil {
		t.Fatal("configured missing file did not fail startup")
	}
}
