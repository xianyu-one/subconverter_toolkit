package app

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"prefetch-proxy/internal/prefetch"
)

func TestRegularSubscriptionsUseInternalURLsCacheAndRefresh(t *testing.T) {
	var fetches atomic.Int32
	var fail atomic.Bool
	var upstreamUA atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamUA.Store(r.Header.Get("User-Agent"))
		n := fetches.Add(1)
		if fail.Load() {
			http.Error(w, "unavailable", http.StatusBadGateway)
			return
		}
		fmt.Fprintf(w, "proxies:\n  - name: node%d\n    type: ss\n    server: node%d.example\n", n, n)
	}))
	defer upstream.Close()

	var gateway *httptest.Server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend-UA", r.Header.Get("User-Agent"))
		if r.URL.Query().Get("list") == "true" {
			link := r.URL.Query().Get("url")
			if !strings.HasPrefix(link, gateway.URL+"/internal/") {
				t.Errorf("backend received original upstream: %s", link)
				http.Error(w, "original URL", http.StatusBadRequest)
				return
			}
			resp, err := http.Get(link)
			if err != nil {
				t.Errorf("read internal subscription: %v", err)
				http.Error(w, "internal fetch failed", http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()
			io.Copy(w, resp.Body)
			return
		}
		w.Write([]byte(r.URL.RawQuery))
	}))
	defer backend.Close()
	service := NewService(&Config{SubconverterURL: backend.URL}, nil)
	handler, err := service.Handler()
	if err != nil {
		t.Fatal(err)
	}
	gateway = httptest.NewServer(handler)
	defer gateway.Close()
	service.cfg.InternalBaseURL = gateway.URL

	request := func(extra string) (int, string, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/sub?url="+url.QueryEscape(upstream.URL)+extra, nil)
		req.Header.Set("User-Agent", "PrivateClient/secret")
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		query, err := url.ParseQuery(resp.Body.String())
		if resp.Code == http.StatusOK && err != nil {
			t.Fatal(err)
		}
		if query.Has("nocache") || query.Has("user_agent") {
			t.Fatalf("proxy-only parameters leaked: %v", query)
		}
		return resp.Code, query.Get("url"), resp.Header().Get("X-Backend-UA")
	}
	read := func(link string) string {
		t.Helper()
		resp, err := http.Get(link)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("internal read: HTTP %d, %v", resp.StatusCode, err)
		}
		return string(body)
	}

	status, defaultLink, backendUA := request("")
	if status != http.StatusOK || !strings.HasPrefix(defaultLink, gateway.URL+"/internal/") || backendUA != prefetch.DefaultUserAgent || upstreamUA.Load() != prefetch.DefaultUserAgent || !strings.Contains(read(defaultLink), "node1.example") {
		t.Fatalf("default request: status=%d link=%q backend UA=%q upstream UA=%v", status, defaultLink, backendUA, upstreamUA.Load())
	}
	status, secondLink, _ := request("")
	if status != http.StatusOK || secondLink != defaultLink || fetches.Load() != 1 {
		t.Fatalf("cache miss: status=%d link=%q fetches=%d", status, secondLink, fetches.Load())
	}
	status, customLink, backendUA := request("&user_agent=Custom%2F2")
	if status != http.StatusOK || customLink == defaultLink || backendUA != "Custom/2" || upstreamUA.Load() != "Custom/2" || fetches.Load() != 2 {
		t.Fatalf("custom UA: status=%d link=%q backend UA=%q upstream UA=%v fetches=%d", status, customLink, backendUA, upstreamUA.Load(), fetches.Load())
	}
	status, refreshedLink, _ := request("&nocache=1")
	if status != http.StatusOK || refreshedLink != defaultLink || fetches.Load() != 3 || !strings.Contains(read(defaultLink), "node3.example") {
		t.Fatalf("refresh: status=%d link=%q fetches=%d", status, refreshedLink, fetches.Load())
	}
	fail.Store(true)
	status, _, _ = request("&nocache=1")
	if status != http.StatusBadGateway || fetches.Load() != 4 || !strings.Contains(read(defaultLink), "node3.example") {
		t.Fatalf("failed refresh replaced cache: status=%d fetches=%d", status, fetches.Load())
	}
	status, _, _ = request("&nocache=0")
	if status != http.StatusOK || fetches.Load() != 4 {
		t.Fatalf("nocache=0 bypassed cache: status=%d fetches=%d", status, fetches.Load())
	}
}

func TestNoURLAndNonHTTPItemsRemainForwardedWithSafeUserAgent(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend-UA", r.Header.Get("User-Agent"))
		w.Write([]byte(r.URL.RawQuery))
	}))
	defer backend.Close()
	handler, err := NewService(&Config{SubconverterURL: backend.URL}, nil).Handler()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/version?user_agent=Safe%2F1&nocache=1", "/sub?url=" + url.QueryEscape("vmess://node") + "&user_agent=Safe%2F1"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("User-Agent", "PrivateClient/secret")
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		query, err := url.ParseQuery(resp.Body.String())
		if err != nil || resp.Code != http.StatusOK || resp.Header().Get("X-Backend-UA") != "Safe/1" || query.Has("user_agent") || query.Has("nocache") {
			t.Fatalf("%s: status=%d query=%v backend UA=%s", path, resp.Code, query, resp.Header().Get("X-Backend-UA"))
		}
	}
}

func TestRegularDomainMarkerUsesConfiguredTTLAndForcedRefresh(t *testing.T) {
	var parses atomic.Int32
	var fail atomic.Bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parses.Add(1)
		if fail.Load() {
			http.Error(w, "unavailable", http.StatusBadGateway)
			return
		}
		w.Write([]byte("proxies:\n  - name: usable\n    server: node.example\n"))
	}))
	defer backend.Close()
	service := NewService(&Config{SubconverterURL: backend.URL, RuleListPath: filepath.Join(t.TempDir(), "rules.list"), CacheTTL: 45 * time.Minute}, nil)
	const source = "https://provider.example/sub"
	const internal = "http://proxy.test/internal/abc"
	const userAgent = "Custom/1"
	for _, force := range []bool{false, false, true} {
		if err := service.processRegularSubscriptions(source, internal, userAgent, force); err != nil {
			t.Fatal(err)
		}
	}
	if parses.Load() != 2 {
		t.Fatalf("domain extraction calls = %d, want 2", parses.Load())
	}
	key := md5Hash("regular_" + userAgent + "\x00" + source)
	item := service.cache.items[key]
	if left := time.Until(item.ExpiresAt); left < 44*time.Minute || left > 45*time.Minute {
		t.Fatalf("marker TTL = %s", left)
	}
	fail.Store(true)
	if err := service.processRegularSubscriptions(source, internal, userAgent, true); err == nil || service.cache.Get(key) == nil {
		t.Fatalf("failed forced extraction replaced marker: %v", err)
	}
}
