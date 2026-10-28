package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"prefetch-proxy/internal/coverconfig"
	"prefetch-proxy/internal/privateconfig"
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

func TestReloadFileConfigKeepsCacheAndPreviousConfigOnFailure(t *testing.T) {
	dir := t.TempDir()
	privatePath := filepath.Join(dir, "private.yaml")
	coverPath := filepath.Join(dir, "cover.yaml")
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	private := func(token string) string {
		return "keys:\n  - name: alice\n    token: " + token + "\n"
	}
	cover := func(source string) string {
		return "keys:\n  - name: alice\n    upstreams: {one: '" + source + "'}\n    profiles: {'1': {upstreams: [one]}}\n"
	}
	write(privatePath, private("old-secret"))
	write(coverPath, cover("vmess://old"))
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.URL.RawQuery))
	}))
	defer backend.Close()
	privateNodes, err := privateconfig.Load(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := coverconfig.Load(coverPath, privateNodes)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(&Config{SubconverterURL: backend.URL, PrivateConfigPath: privatePath, CoverProfileConfigPath: coverPath, CoverProfiles: profiles}, privateNodes)
	t.Setenv("CONTROL_SOCKET_PATH", filepath.Join(dir, "control.sock"))
	stopControl, err := serveControl(service)
	if err != nil {
		t.Fatal(err)
	}
	defer stopControl()
	service.cache.Set("retained", []byte("subscription"), time.Hour)
	handler, err := service.Handler()
	if err != nil {
		t.Fatal(err)
	}
	request := func(token string) (int, string) {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/sub?coverprofile=1&chaintoken="+url.QueryEscape(token), nil))
		query, _ := url.ParseQuery(response.Body.String())
		return response.Code, query.Get("url")
	}
	if status, source := request("old-secret"); status != http.StatusOK || source != "vmess://old" {
		t.Fatalf("initial config: %d %q", status, source)
	}
	write(privatePath, private("new-secret"))
	write(coverPath, cover("vmess://new"))
	if err := Reload(); err != nil {
		t.Fatal(err)
	}
	if status, source := request("new-secret"); status != http.StatusOK || source != "vmess://new" {
		t.Fatalf("reloaded config: %d %q", status, source)
	}
	if status, _ := request("old-secret"); status != http.StatusForbidden {
		t.Fatalf("old token status: %d", status)
	}
	if got := service.cache.Get("retained"); string(got) != "subscription" {
		t.Fatalf("cache lost after reload: %q", got)
	}
	write(coverPath, cover("vmess://bad")+"unknown: true\n")
	if err := Reload(); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("invalid config was accepted or not explained: %v", err)
	}
	if status, source := request("new-secret"); status != http.StatusOK || source != "vmess://new" {
		t.Fatalf("failed reload changed config: %d %q", status, source)
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
