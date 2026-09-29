package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
)

type updater struct {
	client   *http.Client
	maxBytes int64
	output   string
}

func newUpdater(cfg config) (*updater, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // Direct connection unless GEOX_SYNC_PROXY is explicitly set.
	if cfg.proxy != "" {
		proxyURL, err := url.Parse(cfg.proxy)
		if err != nil {
			return nil, errors.New("invalid proxy URL")
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   cfg.timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return errors.New("HTTPS source redirected to a non-HTTPS URL")
			}
			return nil
		},
	}
	return &updater{client: client, maxBytes: cfg.maxBytes, output: cfg.outputDir}, nil
}

func (u *updater) close() { u.client.CloseIdleConnections() }

func (u *updater) sync(ctx context.Context, item source) (int64, error) {
	if err := os.MkdirAll(u.output, 0o755); err != nil {
		return 0, fmt.Errorf("create output directory: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, item.url, nil)
	if err != nil {
		return 0, errors.New("invalid source URL")
	}
	response, err := u.client.Do(request)
	if err != nil {
		// Request errors may contain URL credentials or query tokens.
		if errors.Is(err, context.Canceled) {
			return 0, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return 0, context.DeadlineExceeded
		}
		return 0, errors.New("network request failed (check source and proxy)")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP status %d", response.StatusCode)
	}
	if response.ContentLength > u.maxBytes {
		return 0, fmt.Errorf("file exceeds %d byte limit", u.maxBytes)
	}

	temp, err := os.CreateTemp(u.output, "."+item.file+".tmp-*")
	if err != nil {
		return 0, fmt.Errorf("create temporary file: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	bytes, copyErr := io.Copy(temp, io.LimitReader(response.Body, u.maxBytes+1))
	if copyErr != nil {
		temp.Close()
		return 0, fmt.Errorf("download body: %w", copyErr)
	}
	if bytes == 0 {
		temp.Close()
		return 0, errors.New("empty response")
	}
	if bytes > u.maxBytes {
		temp.Close()
		return 0, fmt.Errorf("file exceeds %d byte limit", u.maxBytes)
	}
	if response.ContentLength >= 0 && bytes != response.ContentLength {
		temp.Close()
		return 0, errors.New("incomplete response")
	}
	if err := temp.Chmod(0o644); err != nil {
		temp.Close()
		return 0, fmt.Errorf("set output permissions: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return 0, fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return 0, fmt.Errorf("close temporary file: %w", err)
	}
	if item.mmdb {
		reader, err := maxminddb.Open(tempName)
		if err != nil {
			return 0, errors.New("invalid MMDB file")
		}
		if err := reader.Close(); err != nil {
			return 0, fmt.Errorf("close MMDB file: %w", err)
		}
	}
	target := filepath.Join(u.output, item.file)
	if err := os.Rename(tempName, target); err != nil {
		return 0, fmt.Errorf("replace output file: %w", err)
	}
	return bytes, nil
}

func run(ctx context.Context, cfg config, logf func(string, ...any)) error {
	u, err := newUpdater(cfg)
	if err != nil {
		return err
	}
	defer u.close()
	for {
		failed := false
		for _, item := range cfg.sources {
			if !item.enabled {
				continue
			}
			start := time.Now()
			size, err := u.sync(ctx, item)
			if err != nil {
				failed = true
				logf("%s: sync failed: %v", item.name, err)
			} else {
				logf("%s: updated %s (%d bytes, %s)", item.name, item.file, size, time.Since(start).Round(time.Millisecond))
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
		if cfg.once {
			if failed {
				return errors.New("one or more files failed to sync")
			}
			return nil
		}
		next := time.Now().Add(cfg.interval)
		logf("next sync: %s", next.Format(time.RFC3339))
		timer := time.NewTimer(cfg.interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}
