package privateconfig_test

import (
	"strings"
	"testing"
	"time"

	"prefetch-proxy/internal/privateconfig"
)

func TestLinksAreScopedRepeatableAndExpire(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := privateconfig.NewLinkStore(10*time.Minute, func() time.Time { return now })
	homeID, err := store.Issue([]privateconfig.Node{{"name": "home"}})
	if err != nil {
		t.Fatal(err)
	}
	officeID, err := store.Issue([]privateconfig.Node{{"name": "office"}})
	if err != nil {
		t.Fatal(err)
	}
	if homeID == officeID || strings.Contains(homeID, "home") {
		t.Fatal("link ID is predictable or contains node data")
	}
	for range 2 {
		body, ok := store.Get(homeID)
		if !ok || !strings.Contains(string(body), "home") || strings.Contains(string(body), "office") {
			t.Fatalf("home link returned %q, %v", body, ok)
		}
	}
	if body, ok := store.Get(officeID); !ok || !strings.Contains(string(body), "office") {
		t.Fatalf("office link returned %q, %v", body, ok)
	}
	now = now.Add(10 * time.Minute)
	if _, ok := store.Get(homeID); ok {
		t.Fatal("expired link remained available")
	}
}
