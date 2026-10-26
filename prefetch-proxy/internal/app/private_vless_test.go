package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"prefetch-proxy/internal/privateconfig"
)

func TestPrivateVLESSSurvivesBackendConversion(t *testing.T) {
	const encryption = "mlkem768x25519plus.native.0rtt.ZWg5lsYtiMeJI4IJkfu7zMafIcxpgUwa6ps9cvdx_vaLKsePfGhSQIOcU5hOyhSM4rlGyRFsO2EJ_YIppIFpTVA8qXl9_9NZxNOKdjVHZ-s1wRCL1kcpT9JmEzqqMspbhGdIA3WYfCBuvvBplMRM4OuavmJ-w0LAwUjAEQQabXcx5guIH7IllkIih3Ix4AURwFuMyMeGnQPDqAgA6JhjEviSK5ARd9BdrsBvlWE8_7GXzCuPPfGBiKaR03bNL3nJyDCc5gOQv_A3ixRf-iscsIOx7DB75rNrB5PAdDdMGHEatmJRodWlmKfA0nSXviqqAkZe6Wt4xDIHgZQARGUuUhx_-9Aa02ZxkvN35LcbEkuvO1HIqFRvNbB-gpXPIYsTQPtTj9wQvkzEymSW77K_0xy13OogzBF92veYmny-FUCBaUZsGfcrOmeSXPWo7KGIbIyPwOrDuxWe7EvGUOYAJpOuRtU9G4uQlTFVbNHL22Jm0woRQfGfQWsDUfE9r_J6R-CDK6hZjOUE5so7D4VUQ6qkT1ulbPNbsQOx5ItcKKq9j6cSeIqIixursTyn4Tu7EuQLkrtmzAiUdvNA9DmbA5ifi9eoUTOxbzoFYHWW1_o-37FRJwhunsG1fMAJ7DsX5RvN3QnOlvwHeRaTtbwBiArLuxe58ZcToVUrzGqDwMmUC6EaXBsuh6MZ3mks_HqO-rc3lkZ0G6l7nqEjgnTAftgrYjkCLIlmyTpyMWNDvLcAB3IetuaxkkxOnhaCdkwyH-B2QYxDAGS2DQJyv_RYqPfAO_DOZDUKRHRnKVBnDHUWG8IHshw0CnzFoKayIazH9bm-3qy_LdxVVsmfo3CHECQtuJnBVQGVwzp4DpEA1AG0xzJX0HQEyZaea6sqfOwb1cMa97iyJAR6WSt4n4iUfKhoeGA2GoO-pOA2J1inyIENFeIvebNK4KS7VQOuPIAEY1KXt5i_uxSByWwiFlx9uLgDCHEsbFdNSpfC29ZKvntsjoWlQknB4iZBKTEKfIUo4syyRmxgjkzCoPi7-sa-x4F2xQwHNLO3T3Ee13CMFPOqbXhY57hobtmO70INTpgyZPG3TbVkpHirC6UWTCVXHwh_nyNkR-k32FuePDe2xJdJMLW-RISFwxdVYbE63xlTXEAC4yuZKgdXvrCFeUNSf-cP0OKX_GcrDjVKcQYsUrY5tKB61kEpk4J6rlDMEWZLi3cMU9uKUpCpAABQPwqh_iF6EQZwuIwTJSWi3Ze-b5y5X3WfeTqfaaxsBXjLZ6OBNFqOXIlwWnyxEyDFYXQE0_gbdLx1b0IIoxO0-qgmnxShJnAr-JgFxmIKCDqcIDex0JwveWsycvFeh7g11gjAThtYpEFkR5fFRjIMikg3wROpf4HEHLQO8pBATURXwjylQ9u-jiaWR5kjdryQzamk9dKt6ddjSkUeCdvBK8HH03YxJOJExSq9Jxw4ERaqkCk8RumlJngjcrnJ53Y9uBWiMVd26DRMKNQeydkNmURt_vrH1-Vb3Zxx1WEfFICRWaCOpV9nTTdg-F5fuHMmCb4kx5Dmn-41GP196G-1z8P6Ets"
	config, err := privateconfig.Parse([]byte("proxies:\n  - name: Second-Hop\n    type: vless\n    server: your-vps.example.com\n    port: 37265\n    uuid: e589e3df-8426-4f0f-b7e3-583f2a4d95b4\n    network: tcp\n    groups: [family]\n    tls: false\n    encryption: \"" + encryption + "\"\n    udp: true\nkeys:\n  - name: family\n    token: secret\n    groups: [family]\n"))
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("proxies:\n  - name: upstream\n    type: ss\n    server: upstream.example\n"))
	}))
	defer upstream.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list") == "true" {
			w.Write([]byte("proxies:\n  - name: upstream\n    type: ss\n    server: upstream.example\n"))
			return
		}
		w.Write([]byte("proxies:\n  - name: '🇹🇼 私有 - Second-Hop'\n    type: vmess\n    server: your-vps.example.com\n    port: 37265\n    uuid: e589e3df-8426-4f0f-b7e3-583f2a4d95b4\n    alterId: 0\n    cipher: ''\n  - name: upstream\n    type: ss\n    server: upstream.example\nproxy-groups:\n  - name: private\n    type: select\n    proxies: ['🇹🇼 私有 - Second-Hop']\n"))
	}))
	defer backend.Close()
	handler, err := NewService(&Config{SubconverterURL: backend.URL, InternalBaseURL: "http://proxy.test"}, config).Handler()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/sub?target=clash&url="+url.QueryEscape(upstream.URL)+"&chaintoken=secret", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	var result struct {
		Proxies []privateconfig.Node `yaml:"proxies"`
		Groups  []struct {
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
	}
	if err := yaml.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Proxies) != 2 || result.Proxies[0]["type"] != "vless" || result.Proxies[0]["encryption"] != encryption || result.Proxies[0]["network"] != "tcp" || result.Proxies[0]["udp"] != true || result.Proxies[0]["dialer-proxy"] != "🚀 前置节点池" || result.Proxies[0]["name"] != "🇹🇼 私有 - Second-Hop" {
		t.Fatalf("VLESS node not restored: %v", result.Proxies)
	}
	if strings.Contains(response.Body.String(), "alterId") || strings.Contains(response.Body.String(), "cipher:") || strings.Contains(response.Body.String(), "groups: [family]") || len(result.Groups) != 1 || len(result.Groups[0].Proxies) != 1 || result.Groups[0].Proxies[0] != "🇹🇼 私有 - Second-Hop" {
		t.Fatalf("converted output contains wrong fields or lost group reference: %s", response.Body.String())
	}
}

func TestRestorePrivateVLESSFromMetaCubeXOutput(t *testing.T) {
	original := privateconfig.Node{
		"name": "🔒私有 - Second-Hop", "type": "vless", "server": "vless.example.com",
		"port": 443, "uuid": "123e4567-e89b-12d3-a456-426614174000",
		"tls": false, "encryption": "mlkem768x25519plus.native.0rtt.example",
		"dialer-proxy": "🚀 前置节点池",
	}
	backend := []byte("proxies:\n  - name: '🔒私有 - Second-Hop'\n    type: vless\n    server: vless.example.com\n    port: 443\n    uuid: 123e4567-e89b-12d3-a456-426614174000\n    tls: true\n")
	patched, err := restorePrivateVLESS(backend, []privateconfig.Node{original})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Proxies []privateconfig.Node `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(patched, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Proxies) != 1 || result.Proxies[0]["tls"] != false || result.Proxies[0]["encryption"] != original["encryption"] {
		t.Fatalf("MetaCubeX output was not repaired: %s", patched)
	}
}

func TestRestorePrivateVLESSRejectsMissingNode(t *testing.T) {
	_, err := restorePrivateVLESS([]byte("proxies: []\n"), []privateconfig.Node{{"name": "private", "type": "vless", "server": "vless.example.com", "port": 443, "uuid": "id"}})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing private node should fail: %v", err)
	}
}
