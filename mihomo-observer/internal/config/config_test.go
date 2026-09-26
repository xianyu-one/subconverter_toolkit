package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPasswordRequiredEvenOnLoopback(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if e := os.WriteFile(p, []byte("mihomo:\n  api: http://127.0.0.1:9090\ndatabase:\n  path: observer.db\nreporting_timezone: UTC\nweb:\n  listen: 127.0.0.1:8080\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := Load(p); e == nil {
		t.Fatal("accepted listener without password")
	}
}
