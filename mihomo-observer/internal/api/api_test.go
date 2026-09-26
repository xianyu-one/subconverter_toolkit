package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"mihomo-observer/internal/collector"
	"mihomo-observer/internal/storage/sqlite"
)

func TestAPIRequiresAuthentication(t *testing.T) {
	s, e := sqlite.Open(filepath.Join(t.TempDir(), "auth.db"), "UTC")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	h := New(s, "secret", func() collector.Status { return collector.Status{} }).Handler()
	r := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d", w.Code)
	}
	r = httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	r.SetBasicAuth("admin", "wrong")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d", w.Code)
	}
	r = httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	r.SetBasicAuth("admin", "secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestPageAndAssetsRequireAuthentication(t *testing.T) {
	s, err := sqlite.Open(filepath.Join(t.TempDir(), "auth.db"), "UTC")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := New(s, "secret", func() collector.Status { return collector.Status{} }).Handler()
	for _, path := range []string{"/", "/assets/app.js", "/api/targets", "/api/history/domain/example.invalid", "/api/problem-history/domain/example.invalid?start_ms=1&end_ms=2"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s without auth: %d", path, w.Code)
		}
		r.SetBasicAuth("admin", "secret")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s with auth: %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestRecentCollectionErrorHidesOldCurrentCount(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status collector.Status
		reason string
	}{
		{"controller error", collector.Status{LastCommitMS: 100, LastReadErrorMS: 101, LastReadError: "controller status 401"}, "controller status 401"},
		{"mailbox drop", collector.Status{LastCommitMS: 100, LastDropMS: 101}, "mailbox_overflow"},
		{"writer error", collector.Status{LastCommitMS: 100, LastWriteErrorMS: 101}, "writer_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := map[string]any{"current_connections": 7}
			applyLiveInterruption(v, tc.status)
			if _, ok := v["current_connections"]; ok {
				t.Fatalf("old count still visible: %v", v)
			}
			if v["interruption_reason"] != tc.reason {
				t.Fatalf("reason=%v", v["interruption_reason"])
			}
		})
	}
	v := map[string]any{"current_connections": 7}
	applyLiveInterruption(v, collector.Status{LastCommitMS: 102, LastReadErrorMS: 101, LastReadError: "controller status 401"})
	if v["current_connections"] != 7 {
		t.Fatalf("recovered count was hidden: %v", v)
	}
}
