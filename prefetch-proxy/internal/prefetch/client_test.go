package prefetch_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"prefetch-proxy/internal/prefetch"
)

func TestFetchPreNodesReadsClashSubscription(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("proxies:\n  - name: upstream\n    type: ss\n    server: vpn.example\n"))
	}))
	defer upstream.Close()
	client := prefetch.NewClient(prefetch.Config{}, func(string, ...interface{}) {}, func(s string) string { return s })
	nodes, err := client.FetchPreNodes(upstream.URL)
	if err != nil || len(nodes) != 1 || nodes[0]["server"] != "vpn.example" {
		t.Fatalf("upstream nodes = %v, %v", nodes, err)
	}
}
