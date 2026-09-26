package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"mihomo-observer/internal/mihomo"
	"mihomo-observer/internal/storage"
)

type Status struct {
	LastReadMS        int64  `json:"last_read_ms"`
	LastCommitMS      int64  `json:"last_commit_ms"`
	LastReadErrorMS   int64  `json:"last_read_error_ms"`
	LastReadError     string `json:"last_read_error,omitempty"`
	LastDropMS        int64  `json:"last_drop_ms"`
	LastWriteErrorMS  int64  `json:"last_write_error_ms"`
	Dropped           int64  `json:"dropped_frames"`
	ReadErrors        int64  `json:"read_errors"`
	WriteErrors       int64  `json:"write_errors"`
	ControllerVersion string `json:"controller_version,omitempty"`
}
type Collector struct {
	base          string
	secret        string
	interval      time.Duration
	client        *http.Client
	store         storage.Store
	status        Status
	version       atomic.Value
	lastReadError atomic.Value
}

func New(base, secret string, interval time.Duration, store storage.Store) *Collector {
	c := &Collector{base: strings.TrimRight(base, "/"), secret: secret, interval: interval, client: &http.Client{Timeout: 8 * time.Second}, store: store}
	c.version.Store("")
	c.lastReadError.Store("")
	return c
}
func (c *Collector) Status() Status {
	return Status{LastReadMS: atomic.LoadInt64(&c.status.LastReadMS), LastCommitMS: atomic.LoadInt64(&c.status.LastCommitMS), LastReadErrorMS: atomic.LoadInt64(&c.status.LastReadErrorMS), LastReadError: c.lastReadError.Load().(string), LastDropMS: atomic.LoadInt64(&c.status.LastDropMS), LastWriteErrorMS: atomic.LoadInt64(&c.status.LastWriteErrorMS), Dropped: atomic.LoadInt64(&c.status.Dropped), ReadErrors: atomic.LoadInt64(&c.status.ReadErrors), WriteErrors: atomic.LoadInt64(&c.status.WriteErrors), ControllerVersion: c.version.Load().(string)}
}
func (c *Collector) fetchVersion(ctx context.Context) {
	u, err := url.Parse(c.base)
	if err != nil {
		return
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/version"
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return
	}
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return
	}
	var v struct {
		Version string `json:"version"`
		Meta    bool   `json:"meta"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&v); err != nil || v.Version == "" {
		return
	}
	c.version.Store(v.Version)
	_ = c.store.SetControllerVersion(ctx, v.Version, v.Meta)
}
func (c *Collector) fetch(ctx context.Context) (mihomo.Frame, error) {
	u, err := url.Parse(c.base)
	if err != nil {
		return mihomo.Frame{}, err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/connections"
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return mihomo.Frame{}, err
	}
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return mihomo.Frame{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return mihomo.Frame{}, fmt.Errorf("controller status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20+1))
	if err != nil {
		return mihomo.Frame{}, err
	}
	if len(b) > 16<<20 {
		return mihomo.Frame{}, errors.New("connections response too large")
	}
	frame, err := mihomo.DecodeFrame(b, time.Now())
	if err != nil {
		return mihomo.Frame{}, fmt.Errorf("invalid snapshot: %w", err)
	}
	return frame, nil
}

type item struct {
	frame   mihomo.Frame
	broken  bool
	reason  string
	dropped int64
}

func (c *Collector) Run(ctx context.Context) {
	c.fetchVersion(ctx)
	mailbox := make(chan item, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		var last int64
		broken := true
		for it := range mailbox {
			continuous := !broken && !it.broken && last > 0 && it.frame.ReceivedAt >= last && it.frame.ReceivedAt-last <= int64(c.interval/time.Millisecond)*3
			writeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if it.reason != "" {
				start := last
				if start == 0 {
					start = it.frame.ReceivedAt
				}
				if err := c.store.RecordGap(writeCtx, "connections", it.reason, start, it.frame.ReceivedAt, it.dropped); err != nil {
					atomic.AddInt64(&c.status.WriteErrors, 1)
					atomic.StoreInt64(&c.status.LastWriteErrorMS, time.Now().UnixMilli())
					broken = true
					slog.Error("collection gap commit failed", "error", err)
				}
			}
			err := c.store.ApplyFrame(writeCtx, it.frame, continuous)
			cancel()
			if err != nil {
				atomic.AddInt64(&c.status.WriteErrors, 1)
				atomic.StoreInt64(&c.status.LastWriteErrorMS, time.Now().UnixMilli())
				broken = true
				slog.Error("connection frame commit failed", "error", err)
				continue
			}
			last = it.frame.ReceivedAt
			broken = false
			atomic.StoreInt64(&c.status.LastCommitMS, last)
		}
	}()
	defer func() { close(mailbox); <-done }()
	broken := false
	breakReason := ""
	nextVersion := time.Now().Add(time.Hour)
	failures := 0
	for {
		if time.Now().After(nextVersion) {
			c.fetchVersion(ctx)
			nextVersion = time.Now().Add(time.Hour)
		}
		frame, err := c.fetch(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			atomic.AddInt64(&c.status.ReadErrors, 1)
			atomic.StoreInt64(&c.status.LastReadErrorMS, time.Now().UnixMilli())
			reason := "controller_request_failed"
			if strings.HasPrefix(err.Error(), "controller status ") {
				reason = err.Error()
			}
			if strings.HasPrefix(err.Error(), "invalid snapshot:") {
				reason = "invalid_snapshot"
			}
			c.lastReadError.Store(reason)
			broken = true
			breakReason = "controller_read_failed"
			if failures < 5 {
				failures++
			}
			slog.Warn("controller snapshot failed", "error", err)
		} else {
			failures = 0
			atomic.StoreInt64(&c.status.LastReadMS, frame.ReceivedAt)
			it := item{frame: frame, broken: broken, reason: breakReason}
			broken = false
			breakReason = ""
			select {
			case mailbox <- it:
			default:
				select {
				case old := <-mailbox:
					it.dropped += old.dropped
				default:
				}
				atomic.AddInt64(&c.status.Dropped, 1)
				atomic.StoreInt64(&c.status.LastDropMS, time.Now().UnixMilli())
				it.broken = true
				it.reason = "mailbox_overflow"
				it.dropped++
				select {
				case mailbox <- it:
				default:
				}
			}
		}
		wait := c.interval
		if failures > 0 {
			wait = time.Duration(1<<failures) * time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
