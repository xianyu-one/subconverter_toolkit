package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigPrecedenceAndDefaults(t *testing.T) {
	t.Setenv("GEOX_SYNC_INTERVAL", "3h")
	t.Setenv("GEOX_SYNC_GEOIP_URL", "https://env.example/geoip.dat")
	t.Setenv("GEOX_SYNC_MAX_BYTES", "not-a-number")
	t.Setenv("GEOX_SYNC_ONCE", "not-a-bool")
	cfg, err := parseConfig([]string{"--interval", "4h", "--geoip-url", "https://flag.example/geoip.dat", "--geosite-enabled=false", "--max-bytes", "100", "--once"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.interval != 4*time.Hour || cfg.sources[0].url != "https://flag.example/geoip.dat" || cfg.sources[1].enabled || cfg.maxBytes != 100 || !cfg.once {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.sources[2].file != "country.mmdb" || cfg.sources[3].file != "GeoLite2-ASN.mmdb" {
		t.Fatalf("unexpected MMDB filenames: %+v", cfg.sources)
	}
}

func TestSyncKeepsOldFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "geoip.dat")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("new data")) }))
	defer server.Close()
	cfg := config{outputDir: dir, timeout: time.Second, maxBytes: 4}
	u, err := newUpdater(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer u.close()
	item := source{name: "geoip", file: "geoip.dat", url: server.URL, enabled: true}
	if _, err := u.sync(context.Background(), item); err == nil {
		t.Fatal("expected size limit error")
	}
	old, err := os.ReadFile(target)
	if err != nil || string(old) != "old" {
		t.Fatalf("old file changed: %q, %v", old, err)
	}
	cfg.maxBytes = 100
	u.maxBytes = cfg.maxBytes
	if _, err := u.sync(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(target)
	if err != nil || string(updated) != "new data" {
		t.Fatalf("file not replaced: %q, %v", updated, err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("unexpected output permissions: %v, %v", info, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v, %v", entries, err)
	}
}

func TestInvalidMMDBDoesNotReplaceOldFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "country.mmdb")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("not a database")) }))
	defer server.Close()
	u, err := newUpdater(config{outputDir: dir, timeout: time.Second, maxBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer u.close()
	_, err = u.sync(context.Background(), source{name: "mmdb", file: "country.mmdb", url: server.URL, mmdb: true})
	if err == nil || !strings.Contains(err.Error(), "invalid MMDB") {
		t.Fatalf("unexpected error: %v", err)
	}
	old, err := os.ReadFile(target)
	if err != nil || string(old) != "old" {
		t.Fatalf("old file changed: %q, %v", old, err)
	}
}

func TestSOCKS5UsesRemoteDNSAndAuthentication(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	hostCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		header := make([]byte, 2)
		if _, err := io.ReadFull(reader, header); err != nil {
			errCh <- err
			return
		}
		methods := make([]byte, int(header[1]))
		if _, err := io.ReadFull(reader, methods); err != nil {
			errCh <- err
			return
		}
		if header[0] != 5 || !containsByte(methods, 2) {
			errCh <- fmt.Errorf("missing SOCKS5 password method: %v", methods)
			return
		}
		_, _ = conn.Write([]byte{5, 2})
		authHeader := make([]byte, 2)
		if _, err := io.ReadFull(reader, authHeader); err != nil {
			errCh <- err
			return
		}
		user := make([]byte, int(authHeader[1]))
		if _, err := io.ReadFull(reader, user); err != nil {
			errCh <- err
			return
		}
		var passwordLength [1]byte
		if _, err := io.ReadFull(reader, passwordLength[:]); err != nil {
			errCh <- err
			return
		}
		password := make([]byte, int(passwordLength[0]))
		if _, err := io.ReadFull(reader, password); err != nil {
			errCh <- err
			return
		}
		if string(user) != "user" || string(password) != "secret" {
			errCh <- fmt.Errorf("bad credentials")
			return
		}
		_, _ = conn.Write([]byte{1, 0})
		request := make([]byte, 5)
		if _, err := io.ReadFull(reader, request); err != nil {
			errCh <- err
			return
		}
		if request[0] != 5 || request[1] != 1 || request[3] != 3 {
			errCh <- fmt.Errorf("expected domain in SOCKS5 request: %v", request)
			return
		}
		host := make([]byte, int(request[4]))
		if _, err := io.ReadFull(reader, host); err != nil {
			errCh <- err
			return
		}
		var port [2]byte
		if _, err := io.ReadFull(reader, port[:]); err != nil {
			errCh <- err
			return
		}
		if binary.BigEndian.Uint16(port[:]) != 80 {
			errCh <- fmt.Errorf("unexpected port")
			return
		}
		hostCh <- string(host)
		_, _ = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80})
		_, _ = reader.ReadString('\n')
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				errCh <- err
				return
			}
			if line == "\r\n" {
				break
			}
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\ndata"))
	}()
	dir := t.TempDir()
	u, err := newUpdater(config{outputDir: dir, timeout: 2 * time.Second, maxBytes: 100, proxy: "socks5h://user:secret@" + listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	defer u.close()
	_, err = u.sync(context.Background(), source{name: "geoip", file: "geoip.dat", url: "http://no-local-dns.invalid/geoip.dat"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case host := <-hostCh:
		if host != "no-local-dns.invalid" {
			t.Fatalf("proxy received host %q", host)
		}
	case err := <-errCh:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("proxy did not receive target domain")
	}
}

func containsByte(values []byte, value byte) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
