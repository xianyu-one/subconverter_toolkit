package privateconfig_test

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
	"prefetch-proxy/internal/privateconfig"
)

func TestLinksAreScopedRepeatableAndExpire(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := privateconfig.NewLinkStore(10*time.Minute, func() time.Time { return now })
	homeID, err := store.Issue([]privateconfig.Node{{"name": "home"}})
	if err != nil {
		t.Fatal(err)
	}
	officeID, err := store.Issue([]privateconfig.Node{{"name": "office"}})
	if err != nil {
		t.Fatal(err)
	}
	if homeID == officeID || strings.Contains(homeID, "home") {
		t.Fatal("link ID is predictable or contains node data")
	}
	for range 2 {
		body, ok := store.Get(homeID)
		if !ok || !strings.Contains(string(body), "home") || strings.Contains(string(body), "office") {
			t.Fatalf("home link returned %q, %v", body, ok)
		}
	}
	if body, ok := store.Get(officeID); !ok || !strings.Contains(string(body), "office") {
		t.Fatalf("office link returned %q, %v", body, ok)
	}
	now = now.Add(10 * time.Minute)
	if _, ok := store.Get(homeID); ok {
		t.Fatal("expired link remained available")
	}
}

func TestVLESSLinkPreservesNestedOptions(t *testing.T) {
	config, err := privateconfig.Parse([]byte(`proxies:
  - name: home
    type: vless
    server: vless.example.com
    port: 443
    uuid: 123e4567-e89b-12d3-a456-426614174000
    tls: true
    reality-opts:
      public-key: example-public-key
      short-id: "0a"
keys:
  - name: family
    token: secret
    nodes: [home]
`))
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := config.Select("secret")
	store := privateconfig.NewLinkStore(time.Minute, time.Now)
	id, err := store.Issue(nodes)
	if err != nil {
		t.Fatal(err)
	}
	body, ok := store.Get(id)
	if !ok {
		t.Fatal("issued link not found")
	}
	var result struct {
		Proxies []privateconfig.Node `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Proxies) != 1 || result.Proxies[0]["type"] != "vless" || result.Proxies[0]["uuid"] != "123e4567-e89b-12d3-a456-426614174000" || result.Proxies[0]["dialer-proxy"] != "🚀 前置节点池" {
		t.Fatalf("VLESS link changed: %s", body)
	}
	opts, ok := result.Proxies[0]["reality-opts"].(privateconfig.Node)
	if !ok || opts["public-key"] != "example-public-key" || opts["short-id"] != "0a" {
		t.Fatalf("Reality options changed: %s", body)
	}
}
