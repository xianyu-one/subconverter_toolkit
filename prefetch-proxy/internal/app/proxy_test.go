package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"prefetch-proxy/internal/coverconfig"
	"prefetch-proxy/internal/privateconfig"
)

func TestPrivateNodesAreSelectedThroughProxy(t *testing.T) {
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
		req := httptest.NewRequest(http.MethodGet, "/sub?url=https%3A%2F%2Fprovider.example%2Fsub&chaintoken="+tc.token, nil)
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
		if len(parts) != 2 || !strings.HasPrefix(parts[1], "http://proxy.test/internal/private/") || strings.Contains(parts[1], tc.token) {
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
	handler.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/sub?url=https%3A%2F%2Fprovider.example%2Fsub&chaintoken=wrong", nil))
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
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.URL.RawQuery))
	}))
	defer backend.Close()
	keys, err := privateconfig.Parse([]byte("keys:\n  - name: alice\n    token: secret\n"))
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := coverconfig.Parse([]byte(`keys:
  - name: alice
    upstreams:
      one: https://one.example/sub
      two: https://two.example/sub
    profiles:
      "1":
        upstreams: [one, two]
        params: {target: clash, filename: abc, exclude: old}
`), keys)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(&Config{SubconverterURL: backend.URL, CoverProfiles: profiles}, keys)
	handler, err := service.Handler()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ path, wantURL, wantFilename, wantExclude string }{
		{"/sub?chaintoken=secret&coverprofile=1", "https://one.example/sub|https://two.example/sub", "abc", "old"},
		{"/sub?chaintoken=secret&coverprofile=1&url=https%3A%2F%2Foverride.example%2Fsub&filename=client&exclude=", "https://override.example/sub", "client", ""},
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
		if query.Get("url") != tc.wantURL || query.Get("filename") != tc.wantFilename || query.Get("exclude") != tc.wantExclude || query.Get("target") != "clash" || query.Has("chaintoken") || query.Has("coverprofile") {
			t.Fatalf("unexpected forwarded query: %v", query)
		}
	}
	legacy := httptest.NewRecorder()
	handler.ServeHTTP(legacy, httptest.NewRequest(http.MethodGet, "/sub?chaintoken=secret&url=https%3A%2F%2Flegacy.example%2Fsub", nil))
	legacyQuery, _ := url.ParseQuery(legacy.Body.String())
	if legacy.Code != http.StatusOK || legacyQuery.Get("url") != "https://legacy.example/sub" || legacyQuery.Has("target") {
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

func TestFailedPrefetchDoesNotBlockAnotherUpstream(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(r.URL.Query().Get("url"))) }))
	defer backend.Close()
	handler, err := NewService(&Config{SubconverterURL: backend.URL, TargetDomains: []string{"127.0.0.1"}}, nil).Handler()
	if err != nil {
		t.Fatal(err)
	}
	bad := "http://127.0.0.1:1/broken"
	good := "https://healthy.example/sub"
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/sub?target=clash&url="+url.QueryEscape(bad+"|"+good), nil))
	if resp.Code != http.StatusOK || resp.Body.String() != good {
		t.Fatalf("partial failure = %d %q", resp.Code, resp.Body.String())
	}
	allFailed := httptest.NewRecorder()
	handler.ServeHTTP(allFailed, httptest.NewRequest(http.MethodGet, "/sub?target=clash&url="+url.QueryEscape(bad), nil))
	if allFailed.Code == http.StatusOK {
		t.Fatalf("all failed unexpectedly succeeded: %q", allFailed.Body.String())
	}
}

func TestPrivateNodesCannotMaskFailedUpstreams(t *testing.T) {
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
	handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/sub?target=clash&url=https%3A%2F%2Fbroken.example%2Fsub&chaintoken=secret", nil))
	if resp.Code == http.StatusOK {
		t.Fatalf("private-only response unexpectedly succeeded: %q", resp.Body.String())
	}
}

func TestCachedPrefetchWithoutNodesIsSkipped(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list") == "true" {
			w.Write([]byte("proxies: []\n"))
			return
		}
		w.Write([]byte(r.URL.Query().Get("url")))
	}))
	defer backend.Close()
	bad := "https://cached.example/sub"
	good := "https://healthy.example/sub"
	service := NewService(&Config{SubconverterURL: backend.URL, InternalBaseURL: "http://proxy.test", TargetDomains: []string{"cached.example"}}, nil)
	service.cache.Set(md5Hash(bad), []byte("not node data"), time.Hour)
	handler, err := service.Handler()
	if err != nil {
		t.Fatal(err)
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/sub?target=clash&url="+url.QueryEscape(bad+"|"+good), nil))
	if resp.Code != http.StatusOK || resp.Body.String() != good {
		t.Fatalf("invalid cached upstream was forwarded: %d %q", resp.Code, resp.Body.String())
	}
}
