package main

import "testing"

func TestDefaultConfigPath(t *testing.T) {
	t.Setenv("MIHOMO_OBSERVER_CONFIG", "")
	if got := defaultConfigPath(); got != "config.yaml" {
		t.Fatalf("empty environment: got %q", got)
	}
	t.Setenv("MIHOMO_OBSERVER_CONFIG", "/etc/observer/custom.yaml")
	if got := defaultConfigPath(); got != "/etc/observer/custom.yaml" {
		t.Fatalf("configured environment: got %q", got)
	}
}
