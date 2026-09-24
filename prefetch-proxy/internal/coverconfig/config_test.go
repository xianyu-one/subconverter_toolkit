package coverconfig_test

import (
	"strings"
	"testing"

	"prefetch-proxy/internal/coverconfig"
	"prefetch-proxy/internal/privateconfig"
)

func TestParseSelectsNamedUpstreamsInProfileOrder(t *testing.T) {
	keys, err := privateconfig.Parse([]byte("keys:\n  - name: alice\n    token: secret\n"))
	if err != nil {
		t.Fatal(err)
	}
	config, err := coverconfig.Parse([]byte(`keys:
  - name: alice
    upstreams:
      first: https://first.example/sub
      second: ss://example
    profiles:
      "1":
        upstreams: [second, first]
        params:
          target: clash
          filename: abc
`), keys)
	if err != nil {
		t.Fatal(err)
	}
	profile, ok := config.Select("alice", "1")
	if !ok || strings.Join(profile.Upstreams, "|") != "ss://example|https://first.example/sub" || profile.Params["filename"] != "abc" {
		t.Fatalf("profile = %#v, %v", profile, ok)
	}
}

func TestParseRejectsInvalidProfiles(t *testing.T) {
	keys, _ := privateconfig.Parse([]byte("keys:\n  - name: alice\n    token: secret\n"))
	for _, tc := range []struct{ name, yaml string }{
		{"unknown key", "keys:\n  - name: bob\n    upstreams: {one: 'https://one.example/sub'}\n    profiles: {'1': {upstreams: [one]}}\n"},
		{"unknown upstream", "keys:\n  - name: alice\n    upstreams: {one: 'https://one.example/sub'}\n    profiles: {'1': {upstreams: [missing]}}\n"},
		{"empty profile", "keys:\n  - name: alice\n    upstreams: {one: 'https://one.example/sub'}\n    profiles: {'1': {upstreams: []}}\n"},
		{"reserved parameter", "keys:\n  - name: alice\n    upstreams: {one: 'https://one.example/sub'}\n    profiles: {'1': {upstreams: [one], params: {url: x}}}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := coverconfig.Parse([]byte(tc.yaml), keys); err == nil {
				t.Fatal("invalid profile accepted")
			}
		})
	}
}
