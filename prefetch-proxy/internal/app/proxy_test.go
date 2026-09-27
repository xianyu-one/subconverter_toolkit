package app

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"prefetch-proxy/internal/coverconfig"
	"prefetch-proxy/internal/privateconfig"
)

func TestPrivateNodesAreSelectedThroughProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("proxies:\n  - name: subscription\n    type: ss\n    server: upstream.example\n"))
	}))
	defer upstream.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list") == "true" {
			w.Write([]byte("proxies:\n  - name: usable\n    type: ss\n    server: example.com\n"))
			return
		}
		w.Write([]byte(r.URL.RawQuery))
	}))
	defer backend.Close()

	var err error
	privateNodes, err := privateconfig.Parse([]byte(`proxies:
  - name: home
    groups: [family]
    type: ss
    server: home.example
  - name: office
    groups: [work]
    type: ss
    server: office.example
keys:
  - name: alice
    token: alice-secret
    groups: [family]
  - name: bob
    token: bob-secret
    groups: [work]
`))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(&Config{SubconverterURL: backend.URL, InternalBaseURL: "http://proxy.test"}, privateNodes)
	handler, err := service.Handler()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ token, want, reject string }{
		{"alice-secret", "home.example", "office.example"},
		{"bob-secret", "office.example", "home.example"},
	} {
		req := httptest.NewRequest(http.MethodGet, "/sub?url="+url.QueryEscape(upstream.URL)+"&chaintoken="+tc.token, nil)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("%s: status %d", tc.token, resp.Code)
		}
		forwarded, err := url.ParseQuery(resp.Body.String())
		if err != nil {
			t.Fatal(err)
		}
		if forwarded.Has("chaintoken") {
			t.Fatal("secret forwarded to backend")
		}
		parts := strings.Split(forwarded.Get("url"), "|")
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "http://proxy.test/internal/") || !strings.HasPrefix(parts[1], "http://proxy.test/internal/private/") || strings.Contains(parts[1], tc.token) {
			t.Fatalf("unexpected internal URL: %v", parts)
		}
		internal, _ := url.Parse(parts[1])
		read := httptest.NewRecorder()
		handler.ServeHTTP(read, httptest.NewRequest(http.MethodGet, internal.RequestURI(), nil))
		body, _ := io.ReadAll(read.Result().Body)
		if read.Code != http.StatusOK || !strings.Contains(string(body), tc.want) || strings.Contains(string(body), tc.reject) {
			t.Fatalf("%s: internal response %d %s", tc.token, read.Code, body)
		}
		if read.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("private nodes must not be cached: %v", read.Header())
		}
	}

	for _, path := range []string{"/internal/private", "/internal/private/guess"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s unexpectedly readable: %d", path, response.Code)
		}
	}
	bad := httptest.NewRecorder()
	handler.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/sub?url="+url.QueryEscape(upstream.URL)+"&chaintoken=wrong", nil))
	if bad.Code != http.StatusForbidden {
		t.Fatalf("invalid token status %d", bad.Code)
	}
	withoutURL := httptest.NewRecorder()
	handler.ServeHTTP(withoutURL, httptest.NewRequest(http.MethodGet, "/version?chaintoken=alice-secret", nil))
	if withoutURL.Code != http.StatusOK || strings.Contains(withoutURL.Body.String(), "chaintoken") {
		t.Fatalf("token leaked without url parameter: %d %s", withoutURL.Code, withoutURL.Body.String())
	}
}

func TestCoverProfileUsesDefaultsAndRequestOverrides(t *testing.T) {
	var upstreamUA string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamUA = r.Header.Get("User-Agent")
		w.Write([]byte("proxies:\n  - name: usable\n    type: ss\n    server: upstream.example\n"))
	}))
	defer upstream.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend-UA", r.Header.Get("User-Agent"))
		if r.URL.Query().Get("list") == "true" {
			w.Write([]byte("proxies:\n  - name: usable\n    type: ss\n    server: upstream.example\n"))
			return
		}
		w.Write([]byte(r.URL.RawQuery))
	}))
	defer backend.Close()
	keys, err := privateconfig.Parse([]byte("keys:\n  - name: alice\n    token: secret\n"))
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := coverconfig.Parse([]byte(fmt.Sprintf(`keys:
  - name: alice
    upstreams:
      one: %s/one
      two: %s/two
    profiles:
      "1":
        upstreams: [one, two]
        params: {target: clash, filename: abc, exclude: old, user_agent: 'Profile/1'}
`, upstream.URL, upstream.URL)), keys)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(&Config{SubconverterURL: backend.URL, InternalBaseURL: "http://proxy.test", CoverProfiles: profiles}, keys)
	handler, err := service.Handler()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		path, wantFilename, wantExclude, wantUA string
		wantCount                               int
	}{
		{"/sub?chaintoken=secret&coverprofile=1", "abc", "old", "Profile/1", 2},
		{"/sub?chaintoken=secret&coverprofile=1&url=" + url.QueryEscape(upstream.URL+"/override") + "&filename=client&exclude=&user_agent=URL%2F2", "client", "", "URL/2", 1},
	} {
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if resp.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", tc.path, resp.Code, resp.Body.String())
		}
		query, err := url.ParseQuery(resp.Body.String())
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(query.Get("url"), "|")
		if len(parts) != tc.wantCount || query.Get("filename") != tc.wantFilename || query.Get("exclude") != tc.wantExclude || query.Get("target") != "clash" || query.Has("chaintoken") || query.Has("coverprofile") || query.Has("user_agent") || resp.Header().Get("X-Backend-UA") != tc.wantUA || upstreamUA != tc.wantUA {
			t.Fatalf("unexpected forwarded query: %v", query)
		}
		for _, part := range parts {
			if !strings.HasPrefix(part, "http://proxy.test/internal/") {
				t.Fatalf("upstream was not rewritten: %s", part)
			}
		}
	}
	legacy := httptest.NewRecorder()
	handler.ServeHTTP(legacy, httptest.NewRequest(http.MethodGet, "/sub?chaintoken=secret&url="+url.QueryEscape(upstream.URL+"/legacy"), nil))
	legacyQuery, _ := url.ParseQuery(legacy.Body.String())
	if legacy.Code != http.StatusOK || !strings.HasPrefix(legacyQuery.Get("url"), "http://proxy.test/internal/") || legacyQuery.Has("target") {
		t.Fatalf("legacy request changed: %d %v", legacy.Code, legacyQuery)
	}
}

func TestCoverProfileRejectsMissingAccessAndEmptyURL(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer backend.Close()
	keys, _ := privateconfig.Parse([]byte("keys:\n  - name: alice\n    token: secret\n  - name: bob\n    token: bob-secret\n"))
	profiles, _ := coverconfig.Parse([]byte("keys:\n  - name: alice\n    upstreams: {one: 'https://one.example/sub'}\n    profiles: {'1': {upstreams: [one]}}\n"), keys)
	handler, _ := NewService(&Config{SubconverterURL: backend.URL, CoverProfiles: profiles}, keys).Handler()
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/sub?coverprofile=1", http.StatusForbidden},
		{"/sub?coverprofile=1&chaintoken=bad", http.StatusForbidden},
		{"/sub?coverprofile=1&chaintoken=bob-secret", http.StatusNotFound},
		{"/sub?coverprofile=missing&chaintoken=secret", http.StatusNotFound},
		{"/sub?coverprofile=1&chaintoken=secret&url=", http.StatusBadRequest},
	} {
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if resp.Code != tc.status {
			t.Fatalf("%s: got %d want %d", tc.path, resp.Code, tc.status)
		}
	}
}

func TestCoverProfileNoCacheDefaultCanBeOverridden(t *testing.T) {
	var fetches atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		w.Write([]byte("proxies:\n  - name: usable\n    type: ss\n    server: upstream.example\n"))
	}))
	defer upstream.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list") == "true" {
			w.Write([]byte("proxies:\n  - name: usable\n    type: ss\n    server: upstream.example\n"))
			return
		}
		w.Write([]byte(r.URL.RawQuery))
	}))
	defer backend.Close()
	keys, _ := privateconfig.Parse([]byte("keys:\n  - name: alice\n    token: secret\n"))
	profiles, err := coverconfig.Parse([]byte(fmt.Sprintf("keys:\n  - name: alice\n    upstreams: {one: '%s'}\n    profiles: {'1': {upstreams: [one], params: {nocache: '1'}}}\n", upstream.URL)), keys)
	if err != nil {
		t.Fatal(err)
	}
	handler, _ := NewService(&Config{SubconverterURL: backend.URL, InternalBaseURL: "http://proxy.test", CoverProfiles: profiles}, keys).Handler()
	for _, path := range []string{
		"/sub?chaintoken=secret&coverprofile=1",
		"/sub?chaintoken=secret&coverprofile=1",
		"/sub?chaintoken=secret&coverprofile=1&nocache=0",
	} {
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, path, nil))
		query, _ := url.ParseQuery(resp.Body.String())
		if resp.Code != http.StatusOK || query.Has("nocache") {
			t.Fatalf("%s: status=%d body=%q", path, resp.Code, resp.Body.String())
		}
	}
	if fetches.Load() != 2 {
		t.Fatalf("profile nocache did not refresh exactly twice: %d", fetches.Load())
	}
}

func TestFailedPrefetchDoesNotBlockAnotherUpstream(t *testing.T) {
	goodServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("proxies:\n  - name: usable\n    type: ss\n    server: example.com\n"))
	}))
	defer goodServer.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list") == "true" {
			w.Write([]byte("proxies:\n  - name: usable\n    type: ss\n    server: example.com\n"))
			return
		}
		w.Write([]byte(r.URL.Query().Get("url")))
	}))
	defer backend.Close()
	handler, err := NewService(&Config{SubconverterURL: backend.URL, InternalBaseURL: "http://proxy.test", TargetDomains: []string{"unreachable.invalid"}}, nil).Handler()
	if err != nil {
		t.Fatal(err)
	}
	bad := "http://127.0.0.1:1/broken"
	good := goodServer.URL
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/sub?target=clash&url="+url.QueryEscape(bad+"|"+good), nil))
	if resp.Code != http.StatusOK || !strings.HasPrefix(resp.Body.String(), "http://proxy.test/internal/") {
		t.Fatalf("partial failure = %d %q", resp.Code, resp.Body.String())
	}
	forced := httptest.NewRecorder()
	handler.ServeHTTP(forced, httptest.NewRequest(http.MethodGet, "/sub?target=clash&nocache=1&url="+url.QueryEscape(good+"|"+bad), nil))
	if forced.Code != http.StatusBadGateway {
		t.Fatalf("partial forced refresh returned %d", forced.Code)
	}
	allFailed := httptest.NewRecorder()
	handler.ServeHTTP(allFailed, httptest.NewRequest(http.MethodGet, "/sub?target=clash&url="+url.QueryEscape(bad), nil))
	if allFailed.Code == http.StatusOK {
		t.Fatalf("all failed unexpectedly succeeded: %q", allFailed.Body.String())
	}
}

func TestPrivateNodesCannotMaskFailedUpstreams(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not nodes"))
	}))
	defer upstream.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list") == "true" {
			w.Write([]byte("proxies: []\n"))
			return
		}
		w.Write([]byte(r.URL.RawQuery))
	}))
	defer backend.Close()
	keys, err := privateconfig.Parse([]byte("proxies:\n  - name: home\n    type: ss\n    server: home.example\nkeys:\n  - name: alice\n    token: secret\n    nodes: [home]\n"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewService(&Config{SubconverterURL: backend.URL, InternalBaseURL: "http://proxy.test"}, keys).Handler()
	if err != nil {
		t.Fatal(err)
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/sub?target=clash&url="+url.QueryEscape(upstream.URL)+"&chaintoken=secret", nil))
	if resp.Code == http.StatusOK {
		t.Fatalf("private-only response unexpectedly succeeded: %q", resp.Body.String())
	}
}

func TestCachedPrefetchWithoutNodesIsSkipped(t *testing.T) {
	goodServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("proxies:\n  - name: usable\n    type: ss\n    server: example.com\n"))
	}))
	defer goodServer.Close()
	bad := "https://cached.example/sub"
	badKey := subscriptionCacheKey(bad, "FlClash/v0.8.98 clash-verge Platform/windows", true)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list") == "true" {
			if strings.Contains(r.URL.Query().Get("url"), badKey) {
				w.Write([]byte("proxies: []\n"))
			} else {
				w.Write([]byte("proxies:\n  - name: usable\n    type: ss\n    server: example.com\n"))
			}
			return
		}
		w.Write([]byte(r.URL.Query().Get("url")))
	}))
	defer backend.Close()
	good := goodServer.URL
	service := NewService(&Config{SubconverterURL: backend.URL, InternalBaseURL: "http://proxy.test", TargetDomains: []string{"cached.example"}}, nil)
	service.cache.Set(badKey, []byte("not node data"), time.Hour)
	handler, err := service.Handler()
	if err != nil {
		t.Fatal(err)
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/sub?target=clash&url="+url.QueryEscape(bad+"|"+good), nil))
	if resp.Code != http.StatusOK || !strings.HasPrefix(resp.Body.String(), "http://proxy.test/internal/") || service.cache.Get(badKey) != nil {
		t.Fatalf("invalid cached upstream was forwarded: %d %q", resp.Code, resp.Body.String())
	}
}
