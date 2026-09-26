package geo

import (
	"net/netip"
	"os"
	"testing"

	"mihomo-observer/internal/config"
)

func TestConfiguredOriginAndNodeExitRemainSeparate(t *testing.T) {
	var cfg config.Config
	cfg.Map.Origin.Country = "US"
	cfg.Map.Origin.Region = "US-TX"
	cfg.Map.Nodes = map[string]config.NodeLocation{"relay": {EntryCountry: "TW", ExitCountry: "US", ExitRegion: "US-TX"}}
	r, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Origin() == nil || r.Origin().Accuracy != "region" {
		t.Fatalf("origin=%+v", r.Origin())
	}
	node := r.Node("relay")
	if node["entry"] == nil || node["exit"] == nil || node["entry"].Label == node["exit"].Label {
		t.Fatalf("node=%v", node)
	}
	if r.Node("missing")["exit"] != nil || r.IP("8.8.8.8") != nil {
		t.Fatal("invented location without MMDB or mapping")
	}
}

func TestMMDBCompatibility(t *testing.T) {
	path := os.Getenv("MIHOMO_OBSERVER_TEST_MMDB")
	if path == "" {
		t.Skip("set MIHOMO_OBSERVER_TEST_MMDB to an installed country or city MMDB")
	}
	var cfg config.Config
	cfg.GeoIP.MMDBPath = path
	r, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	point := r.IP("223.5.5.5")
	if point == nil || point.Label == "" || point.Latitude == 0 && point.Longitude == 0 {
		var raw map[string]any
		addr, _ := netip.ParseAddr("223.5.5.5")
		_ = r.reader.Lookup(addr).Decode(&raw)
		t.Fatalf("unexpected GeoIP result: %+v raw=%v", point, raw)
	}
}

func TestMissingMMDBKeepsConfiguredLocations(t *testing.T) {
	var cfg config.Config
	cfg.GeoIP.MMDBPath = "/path/that/does/not/exist.mmdb"
	cfg.Map.Origin.Country = "US"
	r, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Problem() == "" || r.Origin() == nil || r.IP("223.5.5.5") != nil {
		t.Fatalf("resolver=%+v", r)
	}
}
