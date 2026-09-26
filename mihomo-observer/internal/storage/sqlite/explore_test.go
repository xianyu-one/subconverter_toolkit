package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"mihomo-observer/internal/storage"
)

func TestTargetsPageSortsWholeResultBeforePaging(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "list.db"), "UTC")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	day := time.Now().UTC().Truncate(24 * time.Hour).UnixMilli()
	for i := 0; i < 230; i++ {
		_, err = s.db.Exec(`INSERT INTO stats_daily(bucket_start_ms,subject_kind,subject_value,route_id,ip_version,network,observed_connections,observed_upload_bytes,observed_download_bytes,observed_active_ms,sample_count,last_seen_at_ms) VALUES(?,'domain',?,0,4,'tcp',1,0,?,0,1,?)`, day, fmt.Sprintf("d%03d.example", i), i, day+int64(i))
		if err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.TargetsPage(context.Background(), storage.PageOptions{Sort: "bytes", Desc: true, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 230 || first.Items[0]["value"] != "d229.example" {
		t.Fatalf("unexpected first page: total=%d first=%v", first.Total, first.Items[0])
	}
	last, err := s.TargetsPage(context.Background(), storage.PageOptions{Sort: "bytes", Desc: true, Limit: 50, Offset: 200})
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Items) != 30 || last.Items[0]["value"] != "d029.example" {
		t.Fatalf("unexpected last page: %+v", last)
	}
}

func TestFlowPathAndTopologyKeepCandidateDistinctFromObserved(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "flows.db"), "UTC")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Hour).UnixMilli() + 1000
	first := `{"connections":[{"id":"x","start":"2026-01-01T00:00:00Z","upload":10,"download":20,"metadata":{"host":"google.com","destinationIP":"8.8.8.8","network":"tcp"},"rule":"MATCH","chains":["Exit","Selector"]}]}`
	second := `{"connections":[{"id":"x","start":"2026-01-01T00:00:00Z","upload":30,"download":60,"metadata":{"host":"google.com","destinationIP":"8.8.8.8","network":"tcp"},"rule":"MATCH","chains":["Exit","Selector"]}]}`
	if err = s.ApplyFrame(ctx, frame(t, first, base), false); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyFrame(ctx, frame(t, second, base+1000), true); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordTopology(ctx, base, map[string]storage.ProxyInfo{"Exit": {Type: "Vmess", DialerProxy: "Pool"}, "Pool": {Type: "LoadBalance", All: []string{"A", "B"}}}); err != nil {
		t.Fatal(err)
	}
	if err = s.Project(ctx, base+2000); err != nil {
		t.Fatal(err)
	}
	flows, err := s.Flows(ctx, storage.FlowOptions{Start: base - 1000, End: base + 3600000, Target: "google.com"})
	if err != nil {
		t.Fatal(err)
	}
	items := flows["items"].([]map[string]any)
	if len(items) != 1 || items[0]["target"] != "google.com" || items[0]["destination_ip"] != "8.8.8.8" {
		t.Fatalf("unexpected flows: %v", items)
	}
	if got := items[0]["chains"].([]string); len(got) != 2 || got[0] != "Exit" {
		t.Fatalf("chains=%v", got)
	}
	live, err := s.Flows(ctx, storage.FlowOptions{Live: true, Start: base - 1000, End: base + 3600000, Target: "google.com"})
	if err != nil {
		t.Fatal(err)
	}
	liveItems := live["items"].([]map[string]any)
	if len(liveItems) != 1 || liveItems[0]["upload_bytes"] != int64(20) || liveItems[0]["download_bytes"] != int64(40) {
		t.Fatalf("live=%v", liveItems)
	}
	topology, snapshotAt, valid, err := s.TopologyAt(ctx, base+1000)
	if err != nil || snapshotAt != base || valid != base || len(topology["Pool"].All) != 2 {
		t.Fatalf("topology=%v snapshot=%d valid=%d err=%v", topology, snapshotAt, valid, err)
	}
}

func TestTopologyDoesNotBridgeCollectionGap(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "topology-gap.db"), "UTC")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	base := time.Now().UnixMilli()
	state := map[string]storage.ProxyInfo{"Pool": {Type: "LoadBalance", All: []string{"A", "B"}}}
	for _, at := range []int64{base, base + 60000, base + 600000} {
		if err := s.RecordTopology(ctx, at, state); err != nil {
			t.Fatal(err)
		}
	}
	_, start, valid, err := s.TopologyAt(ctx, base+120000)
	if err != nil || start != base || valid != base+60000 {
		t.Fatalf("old snapshot start=%d valid=%d err=%v", start, valid, err)
	}
	_, start, valid, err = s.TopologyAt(ctx, base+600000)
	if err != nil || start != base+600000 || valid != start {
		t.Fatalf("new snapshot start=%d valid=%d err=%v", start, valid, err)
	}
}

func TestFlowSamplesSortAcrossPagedResult(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "samples.db"), "UTC")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	base := time.Now().UnixMilli()
	makeFrame := func(upA, upB int) string {
		return fmt.Sprintf(`{"connections":[{"id":"a","upload":%d,"download":0,"metadata":{"host":"example.com","destinationIP":"8.8.8.8","network":"tcp"},"chains":["Exit"]},{"id":"b","upload":%d,"download":0,"metadata":{"host":"example.com","destinationIP":"1.1.1.1","network":"tcp"},"chains":["Exit"]}]}`, upA, upB)
	}
	if err := s.ApplyFrame(ctx, frame(t, makeFrame(0, 0), base), false); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyFrame(ctx, frame(t, makeFrame(10, 100), base+1000), true); err != nil {
		t.Fatal(err)
	}
	var route int64
	if err := s.db.QueryRow("SELECT route_id FROM connections_raw LIMIT 1").Scan(&route); err != nil {
		t.Fatal(err)
	}
	page, err := s.FlowSamples(ctx, "example.com", route, base, base+2000, storage.PageOptions{Sort: "bytes", Desc: true, Limit: 1})
	if err != nil || page.Total != 2 || page.Items[0]["destination_ip"] != "1.1.1.1" {
		t.Fatalf("first page=%+v err=%v", page, err)
	}
	page, err = s.FlowSamples(ctx, "example.com", route, base, base+2000, storage.PageOptions{Sort: "bytes", Desc: true, Limit: 1, Offset: 1})
	if err != nil || page.Items[0]["destination_ip"] != "8.8.8.8" {
		t.Fatalf("second page=%+v err=%v", page, err)
	}
}

func TestOldFlowUsesRetainedDailyStats(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "old-flow.db"), "UTC")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	day := time.Now().UTC().Add(-200 * 24 * time.Hour).Truncate(24 * time.Hour).UnixMilli()
	_, err = s.db.Exec(`INSERT INTO stats_daily(bucket_start_ms,subject_kind,subject_value,route_id,ip_version,network,observed_connections,observed_upload_bytes,observed_download_bytes,observed_active_ms,sample_count,last_seen_at_ms) VALUES(?,'ip_only','223.5.5.5',1,4,'tcp',2,4,8,100,2,?)`, day, day+1000)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Flows(context.Background(), storage.FlowOptions{Start: day + 500, End: day + 86400000})
	if err != nil {
		t.Fatal(err)
	}
	items := result["items"].([]map[string]any)
	if len(items) != 1 || items[0]["destination_ip"] != "223.5.5.5" || result["range_rounded_to_day"] != true {
		t.Fatalf("old flow=%v", result)
	}
}
