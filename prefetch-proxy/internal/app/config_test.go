package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestCacheTTLConfiguration(t *testing.T) {
	t.Setenv("PRIVATE_CONFIG_PATH", "")
	t.Setenv("COVER_PROFILE_CONFIG_PATH", "")
	for _, tc := range []struct {
		value string
		want  time.Duration
		bad   bool
	}{
		{"3h", 3 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"0h", 0, true},
		{"-1m", 0, true},
		{"abc", 0, true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("PREFETCH_CACHE_TTL", tc.value)
			cfg, _, err := loadConfig()
			if (err != nil) != tc.bad {
				t.Fatalf("loadConfig error = %v", err)
			}
			if !tc.bad && cfg.CacheTTL != tc.want {
				t.Fatalf("TTL = %s, want %s", cfg.CacheTTL, tc.want)
			}
		})
	}
	service := NewService(&Config{}, nil)
	if service.cfg.CacheTTL != 3*time.Hour {
		t.Fatalf("service default TTL = %s", service.cfg.CacheTTL)
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
