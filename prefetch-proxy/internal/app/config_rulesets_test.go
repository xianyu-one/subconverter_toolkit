package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRemoteConfigRulesetsAreServedByPrefetchProxy(t *testing.T) {
	var source *httptest.Server
	source = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/custom.ini":
			w.Write([]byte("[custom]\r\nruleset=DIRECT," + source.URL + "/private.list?token=secret,86400\r\nruleset=REJECT,[]GEOIP,CN,no-resolve\r\nruleset=DIRECT,rules/local.list\r\nother=" + source.URL + "/private.list\r\n"))
		case "/private.list":
			w.Write([]byte("DOMAIN,private.example\n"))
		case "/empty.list":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer source.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.URL.Query().Get("config")))
	}))
	defer backend.Close()
	service := NewService(&Config{SubconverterURL: backend.URL, InternalBaseURL: "http://prefetch.test"}, nil)
	handler, err := service.Handler()
	if err != nil {
		t.Fatal(err)
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/sub?config="+url.QueryEscape(source.URL+"/custom.ini"), nil))
	if resp.Code != http.StatusOK || !strings.HasPrefix(resp.Body.String(), "http://prefetch.test/internal/config/") {
		t.Fatalf("forwarded config = %d %q", resp.Code, resp.Body.String())
	}
	configURL, _ := url.Parse(resp.Body.String())
	configResp := httptest.NewRecorder()
	handler.ServeHTTP(configResp, httptest.NewRequest(http.MethodGet, configURL.RequestURI(), nil))
	config := configResp.Body.String()
	if configResp.Code != http.StatusOK || !strings.Contains(config, "ruleset=REJECT,[]GEOIP,CN,no-resolve\r\n") || !strings.Contains(config, "ruleset=DIRECT,rules/local.list\r\n") || !strings.Contains(config, "other="+source.URL+"/private.list") || strings.Contains(config, "token=secret") {
		t.Fatalf("rewritten config = %d %q", configResp.Code, config)
	}
	line := strings.Split(config, "\r\n")[1]
	field := strings.Split(line, ",")[1]
	if !strings.HasSuffix(line, ",86400") || !strings.HasSuffix(field, "/private.list") {
		t.Fatalf("ruleset options or extension lost: %q", line)
	}
	rulesURL, _ := url.Parse(field)
	rulesResp := httptest.NewRecorder()
	handler.ServeHTTP(rulesResp, httptest.NewRequest(http.MethodGet, rulesURL.RequestURI(), nil))
	if rulesResp.Code != http.StatusOK || rulesResp.Body.String() != "DOMAIN,private.example\n" {
		t.Fatalf("ruleset = %d %q", rulesResp.Code, rulesResp.Body.String())
	}
	second, err := service.prepareConfig(httptest.NewRequest(http.MethodGet, "/", nil), source.URL+"/custom.ini")
	if err != nil {
		t.Fatal(err)
	}
	secondURL, _ := url.Parse(second)
	secondConfig := httptest.NewRecorder()
	handler.ServeHTTP(secondConfig, httptest.NewRequest(http.MethodGet, secondURL.RequestURI(), nil))
	if !strings.Contains(secondConfig.Body.String(), field) {
		t.Fatal("same ruleset source received a different short link")
	}
	sourceRule := source.URL + "/private.list?token=secret"
	service.ruleLinksMu.Lock()
	link := service.ruleLinkIDs[sourceRule]
	link.reuseUntil = time.Now().Add(-time.Second)
	service.ruleLinkIDs[sourceRule] = link
	service.ruleLinksMu.Unlock()
	third, err := service.prepareConfig(httptest.NewRequest(http.MethodGet, "/", nil), source.URL+"/custom.ini")
	if err != nil {
		t.Fatal(err)
	}
	thirdURL, _ := url.Parse(third)
	thirdConfig := httptest.NewRecorder()
	handler.ServeHTTP(thirdConfig, httptest.NewRequest(http.MethodGet, thirdURL.RequestURI(), nil))
	if strings.Contains(thirdConfig.Body.String(), field) {
		t.Fatal("near-expiry ruleset link was reused in a fresh config")
	}
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/internal/ruleset/unknown/private.list", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown ruleset link = %d", missing.Code)
	}
	emptyID, err := service.issueRuleset(source.URL + "/empty.list")
	if err != nil {
		t.Fatal(err)
	}
	empty := httptest.NewRecorder()
	handler.ServeHTTP(empty, httptest.NewRequest(http.MethodGet, "/internal/ruleset/"+emptyID+"/empty.list", nil))
	if empty.Code != http.StatusBadGateway {
		t.Fatalf("empty ruleset = %d", empty.Code)
	}
}

func TestLocalConfigRequiresSharedDirectory(t *testing.T) {
	dir := t.TempDir()
	ruleSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("DOMAIN,local-only.example\n"))
	}))
	defer ruleSource.Close()
	if err := os.Mkdir(filepath.Join(dir, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config", "new.ini"), []byte("ruleset=DIRECT,"+ruleSource.URL+"/private.list\nruleset=DIRECT,[]FINAL\n"), 0600); err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(r.URL.Query().Get("config"))) }))
	defer backend.Close()
	for _, tc := range []struct{ configDir, want string }{{"", "config/new.ini"}, {dir, "http://prefetch.test/internal/config/"}} {
		handler, _ := NewService(&Config{SubconverterURL: backend.URL, InternalBaseURL: "http://prefetch.test", ConfigDir: tc.configDir}, nil).Handler()
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/sub?config=config%2Fnew.ini", nil))
		if resp.Code != http.StatusOK || !strings.HasPrefix(resp.Body.String(), tc.want) {
			t.Fatalf("config dir %q: %d %q", tc.configDir, resp.Code, resp.Body.String())
		}
		if tc.configDir != "" {
			configURL, _ := url.Parse(resp.Body.String())
			rewritten := httptest.NewRecorder()
			handler.ServeHTTP(rewritten, httptest.NewRequest(http.MethodGet, configURL.RequestURI(), nil))
			lines := strings.Split(rewritten.Body.String(), "\n")
			if rewritten.Code != http.StatusOK || !strings.Contains(lines[0], "/internal/ruleset/") || lines[1] != "ruleset=DIRECT,[]FINAL" {
				t.Fatalf("local config rewrite = %d %q", rewritten.Code, rewritten.Body.String())
			}
			ruleURL, _ := url.Parse(strings.TrimPrefix(lines[0], "ruleset=DIRECT,"))
			rule := httptest.NewRecorder()
			handler.ServeHTTP(rule, httptest.NewRequest(http.MethodGet, ruleURL.RequestURI(), nil))
			if rule.Code != http.StatusOK || rule.Body.String() != "DOMAIN,local-only.example\n" {
				t.Fatalf("local config rule = %d %q", rule.Code, rule.Body.String())
			}
		}
	}
	handler, _ := NewService(&Config{SubconverterURL: backend.URL, ConfigDir: dir}, nil).Handler()
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/sub?config=..%2Fsecret.ini", nil))
	if resp.Code != http.StatusBadGateway {
		t.Fatalf("path traversal accepted: %d", resp.Code)
	}
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/sub?config=config%2Fmissing.ini", nil))
	if missing.Code != http.StatusBadGateway {
		t.Fatalf("missing shared config = %d", missing.Code)
	}
}
