package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"prefetch-proxy/internal/privateconfig"
)

func TestPrivateNodesAreSelectedThroughProxy(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
