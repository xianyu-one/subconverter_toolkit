package rules_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"prefetch-proxy/internal/rules"
)

func TestUpdateAddsDomainsOnceToBothFiles(t *testing.T) {
	dir := t.TempDir()
	rulePath := filepath.Join(dir, "rules.list")
	fakePath := filepath.Join(dir, "fake.yaml")
	if err := os.WriteFile(fakePath, []byte("filter:\n  - +.existing.example\n"), 0600); err != nil {
		t.Fatal(err)
	}
	updater := &rules.Updater{RuleListPath: rulePath, FakeIPFilterPath: fakePath}
	data := []byte("proxies:\n  - name: first\n    server: vpn.example\n  - name: second\n    server: vpn.example\n  - name: ip\n    server: 192.0.2.1\n")
	updater.Update(data)
	updater.Update(data)
	rulesData, err := os.ReadFile(rulePath)
	if err != nil {
		t.Fatal(err)
	}
	fakeData, err := os.ReadFile(fakePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(rulesData), "DOMAIN-SUFFIX,vpn.example") != 1 || strings.Contains(string(rulesData), "192.0.2.1") {
		t.Fatalf("unexpected rule list: %s", rulesData)
	}
	if strings.Count(string(fakeData), "  - +.vpn.example") != 1 {
		t.Fatalf("unexpected fake IP filter: %s", fakeData)
	}
}
