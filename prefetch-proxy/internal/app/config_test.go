package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateConfigPathIsOptionalButMustLoadWhenSet(t *testing.T) {
	t.Setenv("PRIVATE_CONFIG_PATH", "")
	t.Setenv("COVER_PROFILE_CONFIG_PATH", "")
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

func TestCoverProfilesLoadAtStartupAndRequireKnownKeys(t *testing.T) {
	dir := t.TempDir()
	privatePath := filepath.Join(dir, "private.yaml")
	coverPath := filepath.Join(dir, "cover.yaml")
	if err := os.WriteFile(privatePath, []byte("keys:\n  - name: alice\n    token: secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(coverPath, []byte("keys:\n  - name: alice\n    upstreams: {one: 'https://one.example/sub'}\n    profiles: {'1': {upstreams: [one]}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRIVATE_CONFIG_PATH", privatePath)
	t.Setenv("COVER_PROFILE_CONFIG_PATH", coverPath)
	cfg, keys, err := loadConfig()
	if err != nil || keys == nil || cfg.CoverProfiles == nil {
		t.Fatalf("profiles did not load: %v", err)
	}
	if err := os.WriteFile(coverPath, []byte("keys:\n  - name: missing\n    upstreams: {one: 'https://one.example/sub'}\n    profiles: {'1': {upstreams: [one]}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadConfig(); err == nil {
		t.Fatal("unknown key accepted at startup")
	}
}
