package collector

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mihomo-observer/internal/mihomo"
	"mihomo-observer/internal/storage"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchUsesBearerAndFullSnapshot(t *testing.T) {
	c := New("http://controller.local:9090", "test-secret", time.Second, nil)
	c.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/connections" || r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Errorf("unexpected controller request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"connections":null}`)), Header: make(http.Header)}, nil
	})}
	f, e := c.fetch(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if f.ReceivedAt == 0 || len(f.Connections) != 0 {
		t.Fatal("missing snapshot timestamp")
	}
}

func TestControllerRejectionHasSafeStatus(t *testing.T) {
	c := New("http://controller.local:9090", "private-secret", time.Second, nil)
	c.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	deadline := time.After(time.Second)
	for c.Status().ReadErrors == 0 {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("read error not reported")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	<-done
	status := c.Status()
	if status.LastReadError == "" || status.LastReadErrorMS == 0 || strings.Contains(status.LastReadError, "private-secret") {
		t.Fatalf("unsafe read status: %+v", status)
	}
}

type overflowStore struct {
	storage.Store
	first   atomic.Bool
	started chan struct{}
	unblock chan struct{}
	gaps    chan struct {
		reason  string
		dropped int64
	}
}

func (s *overflowStore) ApplyFrame(context.Context, mihomo.Frame, bool) error {
	if s.first.CompareAndSwap(false, true) {
		close(s.started)
		<-s.unblock
	}
	return nil
}
func (s *overflowStore) RecordGap(_ context.Context, _, reason string, _, _, dropped int64) error {
	select {
	case s.gaps <- struct {
		reason  string
		dropped int64
	}{reason, dropped}:
	default:
	}
	return nil
}

func TestMailboxOverflowPersistsReasonAndCount(t *testing.T) {
	store := &overflowStore{started: make(chan struct{}), unblock: make(chan struct{}), gaps: make(chan struct {
		reason  string
		dropped int64
	}, 10)}
	c := New("http://controller.local:9090", "", time.Millisecond, store)
	c.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/version" {
			return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"connections":[]}`)), Header: make(http.Header)}, nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	select {
	case <-store.started:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("first frame not applied")
	}
	deadline := time.After(time.Second)
	for c.Status().Dropped == 0 {
		select {
		case <-deadline:
			close(store.unblock)
			cancel()
			<-done
			t.Fatal("mailbox did not overflow")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(store.unblock)
	select {
	case gap := <-store.gaps:
		if gap.reason != "mailbox_overflow" || gap.dropped < 1 {
			t.Fatalf("gap=%+v", gap)
		}
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("overflow gap not persisted")
	}
	cancel()
	<-done
}
