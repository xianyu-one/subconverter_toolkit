package privateconfig_test

import (
	"strings"
	"testing"

	"prefetch-proxy/internal/privateconfig"
)

func TestParseSelectsUnionInNodeOrder(t *testing.T) {
	input := `proxies:
  - name: home
    groups: [shared]
    type: ss
    server: home.example
  - name: office
    groups: [work]
    type: ss
    server: office.example
  - name: spare
    groups: [shared]
    type: ss
    server: spare.example
keys:
  - name: family
    token: family-secret
    nodes: [spare]
    groups: [shared]
  - name: colleague
    token: work-secret
    groups: [work]
`
	config, err := privateconfig.Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	selected, ok := config.Select("family-secret")
	if !ok || len(selected) != 2 {
		t.Fatalf("family selection = %v, %v", selected, ok)
	}
	if selected[0]["name"] != "🔒私有 - home" || selected[1]["name"] != "🔒私有 - spare" {
		t.Fatalf("unexpected node order: %v", selected)
	}
	if _, exists := selected[0]["groups"]; exists {
		t.Fatal("group metadata leaked into Clash node")
	}
	if selected[0]["dialer-proxy"] != "🚀 前置节点池" {
		t.Fatalf("missing dialer-proxy: %v", selected[0])
	}
	other, ok := config.Select("work-secret")
	if !ok || len(other) != 1 || other[0]["name"] != "🔒私有 - office" {
		t.Fatalf("work selection = %v, %v", other, ok)
	}
}

func TestParseRejectsInvalidReferencesWithoutLeakingSecrets(t *testing.T) {
	input := `proxies:
  - name: home
    type: ss
    server: home.example
keys:
  - name: family
    token: highly-sensitive-token
    groups: [missing]
`
	_, err := privateconfig.Parse([]byte(input))
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected missing group error, got %v", err)
	}
	if strings.Contains(err.Error(), "highly-sensitive-token") {
		t.Fatalf("secret leaked in error: %v", err)
	}
}

func TestParseRejectsDuplicateNamesAndTokens(t *testing.T) {
	base := `proxies:
  - name: home
    type: ss
    server: home.example
keys:
  - name: first
    token: secret-a
    nodes: [home]
`
	for _, tc := range []struct {
		name, extra, want string
	}{
		{"duplicate node", "  - name: home\n    type: ss\n    server: other.example\n", "duplicate proxy name"},
		{"duplicate key name", "  - name: first\n    token: secret-b\n    nodes: [home]\n", "duplicate key name"},
		{"duplicate token", "  - name: second\n    token: secret-a\n    nodes: [home]\n", "duplicates another token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := base
			if tc.name == "duplicate node" {
				input = strings.Replace(base, "keys:\n", tc.extra+"keys:\n", 1)
			} else {
				input += tc.extra
			}
			_, err := privateconfig.Parse([]byte(input))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
}

func TestParseRejectsAdditionalYAMLDocuments(t *testing.T) {
	input := `proxies:
  - name: home
    type: ss
    server: home.example
keys:
  - name: first
    token: secret-a
    nodes: [home]
---
keys: []
`
	if _, err := privateconfig.Parse([]byte(input)); err == nil {
		t.Fatal("additional YAML document was ignored")
	}
}

func TestParseErrorGivesLocationWithoutSecret(t *testing.T) {
	input := `proxies:
  - name: home
    type: ss
    server: home.example
keys:
  - name: first
    token: highly-sensitive-token
    nodse: [home]
`
	_, err := privateconfig.Parse([]byte(input))
	if err == nil || !strings.Contains(err.Error(), "line 8") || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected useful location and reason, got %v", err)
	}
	if strings.Contains(err.Error(), "highly-sensitive-token") {
		t.Fatalf("secret leaked: %v", err)
	}
}

func TestSelectionCannotMutateConfiguredNodes(t *testing.T) {
	input := `proxies:
  - name: home
    type: ss
    server: home.example
    plugin-opts:
      host: original.example
keys:
  - name: first
    token: secret-a
    nodes: [home]
`
	config, err := privateconfig.Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := config.Select("secret-a")
	first[0]["server"] = "changed.example"
	first[0]["plugin-opts"].(privateconfig.Node)["host"] = "changed.example"
	second, _ := config.Select("secret-a")
	if second[0]["server"] != "home.example" || second[0]["plugin-opts"].(privateconfig.Node)["host"] != "original.example" {
		t.Fatalf("selection changed stored node: %v", second)
	}
}
