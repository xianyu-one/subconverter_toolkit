package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"mihomo-observer/internal/mihomo"
)

func frame(t *testing.T, body string, ms int64) mihomo.Frame {
	t.Helper()
	f, e := mihomo.DecodeFrame([]byte(body), time.UnixMilli(ms))
	if e != nil {
		t.Fatal(e)
	}
	return f
}
func TestFrameBaselineGapAndProjection(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "test.db"), "UTC")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Hour).UnixMilli() + 1000
	first := `{"connections":[{"id":"x","start":"2026-01-01T00:00:00Z","upload":100,"download":1000,"metadata":{"host":"cdn.example.com","destinationIP":"2001:db8::1","destinationPort":"443","network":"tcp"},"rule":"MATCH","chains":["Proxy"]}],"uploadTotal":200,"downloadTotal":2000}`
	second := `{"connections":[{"id":"x","start":"2026-01-01T00:00:00Z","upload":120,"download":1200,"metadata":{"host":"cdn.example.com","destinationIP":"2001:db8::1","destinationPort":"443","network":"tcp"},"rule":"MATCH","chains":["Proxy"]}],"uploadTotal":230,"downloadTotal":2200}`
	if e = s.ApplyFrame(ctx, frame(t, first, base), false); e != nil {
		t.Fatal(e)
	}
	var initialGaps int
	if e = s.db.QueryRow("SELECT COUNT(*) FROM collection_gaps").Scan(&initialGaps); e != nil || initialGaps != 0 {
		t.Fatalf("first baseline is not a gap: %d %v", initialGaps, e)
	}
	if e = s.ApplyFrame(ctx, frame(t, second, base+1000), true); e != nil {
		t.Fatal(e)
	}
	var down, active int64
	e = s.db.QueryRow("SELECT observed_download_bytes,observed_active_ms FROM connection_hours").Scan(&down, &active)
	if e != nil {
		t.Fatal(e)
	}
	if down != 200 || active != 1000 {
		t.Fatalf("down=%d active=%d", down, active)
	}
	if e = s.ApplyFrame(ctx, frame(t, `{"connections":null}`, base+2000), true); e != nil {
		t.Fatal(e)
	}
	var state string
	e = s.db.QueryRow("SELECT observation_state FROM connections_raw").Scan(&state)
	if e != nil || state != "absent" {
		t.Fatalf("state=%s err=%v", state, e)
	}
	if e = s.Project(ctx, base+3000); e != nil {
		t.Fatal(e)
	}
	detail, e := s.Detail(ctx, "domain", "cdn.example.com")
	if e != nil {
		t.Fatal(e)
	}
	if len(detail["hourly"].([]map[string]any)) == 0 {
		t.Fatal("missing projection")
	}
	if len(detail["daily"].([]map[string]any)) == 0 || len(detail["paths"].([]map[string]any)) == 0 || len(detail["recent_samples"].([]map[string]any)) == 0 {
		t.Fatal("detail is missing daily, path, or sample evidence")
	}
	targets, e := s.Targets(ctx)
	if e != nil || len(targets) != 2 {
		t.Fatalf("targets=%v err=%v", targets, e)
	}
	dash, e := s.Dashboard(ctx, base+3000)
	if e != nil {
		t.Fatal(e)
	}
	if dash["today_observed_download_bytes"] != int64(200) {
		t.Fatalf("traffic: %v", dash)
	}
}

func TestRestartLeavesCurrentCountUnknownAndRecordsGap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	s, err := Open(path, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	at := time.Now().Add(-2 * time.Second).UnixMilli()
	if err = s.ApplyFrame(ctx, frame(t, `{"connections":[{"id":"x","download":10,"metadata":{"destinationIP":"192.0.2.1"}}]}`, at), false); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dash, err := s.Dashboard(ctx, time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := dash["current_connections"]; ok {
		t.Fatalf("restart must leave count unknown: %v", dash)
	}
	if len(dash["recent_gaps"].([]map[string]any)) == 0 {
		t.Fatalf("missing restart gap: %v", dash)
	}
}

func TestLateDomainAndASNReattributeEarlierHour(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "late.db"), "UTC")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour).UnixMilli() + 3599000
	first := `{"connections":[{"id":"x","download":100,"metadata":{"destinationIP":"192.0.2.3","network":"tcp"},"rule":"MATCH","chains":["A"]}]}`
	second := `{"connections":[{"id":"x","download":150,"metadata":{"destinationIP":"192.0.2.3","sniffHost":"late.example","destinationIPASN":"AS64500","network":"tcp"},"rule":"MATCH","chains":["A"]}]}`
	if err = s.ApplyFrame(ctx, frame(t, first, base), false); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyFrame(ctx, frame(t, second, base+2000), true); err != nil {
		t.Fatal(err)
	}
	if err = s.Project(ctx, base+3000); err != nil {
		t.Fatal(err)
	}
	domain, err := s.Detail(ctx, "domain", "late.example")
	if err != nil {
		t.Fatal(err)
	}
	if len(domain["hourly"].([]map[string]any)) != 4 {
		t.Fatalf("expected two reattributed hours across all/path totals: %v", domain["hourly"])
	}
	if len(domain["grouping_changes"].([]map[string]any)) != 1 {
		t.Fatalf("missing IP-to-domain attribution record: %v", domain["grouping_changes"])
	}
	ip, err := s.Detail(ctx, "destination_ip", "192.0.2.3")
	if err != nil || len(ip["hourly"].([]map[string]any)) == 0 {
		t.Fatalf("IP dimension lost: %v %v", ip, err)
	}
	asn, err := s.Detail(ctx, "asn", "AS64500")
	if err != nil || len(asn["hourly"].([]map[string]any)) != 4 {
		t.Fatalf("late ASN not backfilled: %v %v", asn, err)
	}
}

func TestExistingVersionOneDatabaseMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration.db")
	s, err := Open(path, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("DROP TABLE target_changes"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var version int
	if err = s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Fatalf("version=%d err=%v", version, err)
	}
}

func TestDailyHistorySurvivesRawAndHourlyCleanup(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "retention.db"), "UTC")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	at := time.Now().UTC().Add(-20*24*time.Hour).Truncate(time.Hour).UnixMilli() + 1000
	for i, body := range []string{
		`{"connections":[{"id":"x","download":100,"metadata":{"host":"retained.example","destinationIP":"192.0.2.7"},"rule":"MATCH","chains":["Proxy"]}],"uploadTotal":10,"downloadTotal":100}`,
		`{"connections":[{"id":"x","download":170,"metadata":{"host":"retained.example","destinationIP":"192.0.2.7"},"rule":"MATCH","chains":["Proxy"]}],"uploadTotal":10,"downloadTotal":170}`,
		`{"connections":[] ,"uploadTotal":10,"downloadTotal":170}`,
	} {
		if err = s.ApplyFrame(ctx, frame(t, body, at+int64(i)*1000), i != 0); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Project(ctx, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err = s.Cleanup(ctx, time.Now().UnixMilli(), 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	var rawCount int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM connections_raw").Scan(&rawCount); err != nil || rawCount != 0 {
		t.Fatalf("raw=%d err=%v", rawCount, err)
	}
	detail, err := s.Detail(ctx, "domain", "retained.example")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail["hourly"].([]map[string]any)) != 0 || len(detail["daily"].([]map[string]any)) == 0 {
		t.Fatalf("daily retention missing: %v", detail)
	}
	if len(detail["paths"].([]map[string]any)) != 1 {
		t.Fatalf("historical route missing: %v", detail["paths"])
	}
}

func TestDailyHistoryPagesByWholeDay(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "pages.db"), "UTC")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	start := time.Now().UTC().Truncate(24 * time.Hour).UnixMilli()
	for i := 0; i < 100; i++ {
		day := start - int64(i)*86400000
		for _, route := range []int{0, 1} {
			_, err = s.db.Exec(`INSERT INTO stats_daily(bucket_start_ms,subject_kind,subject_value,route_id,ip_version,network,observed_connections,observed_upload_bytes,observed_download_bytes,observed_active_ms,sample_count,last_seen_at_ms) VALUES(?,'domain','pages.example',?,4,'tcp',1,0,1,0,1,?)`, day, route, day+1000)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	first, err := s.DailyHistory(context.Background(), "domain", "pages.example", 1<<63-1)
	if err != nil || len(first) != 180 {
		t.Fatalf("first rows=%d err=%v", len(first), err)
	}
	before := first[len(first)-1]["bucket_start_ms"].(int64)
	second, err := s.DailyHistory(context.Background(), "domain", "pages.example", before)
	if err != nil || len(second) != 20 {
		t.Fatalf("second rows=%d err=%v", len(second), err)
	}
}

func TestProblemOccurrenceSurvivesRawCleanupAndNewerSummary(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "problem-history.db"), "UTC")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	base := time.Now().UTC().Add(-20*24*time.Hour).Truncate(time.Hour).UnixMilli() + 60000
	for burst := 0; burst < 2; burst++ {
		connections := make([]map[string]any, 12)
		for i := range connections {
			connections[i] = map[string]any{"id": fmt.Sprintf("old-%d-%d", burst, i), "metadata": map[string]any{"host": "history.example", "destinationIP": "192.0.2.2", "destinationPort": 443, "network": "tcp"}, "rule": "MATCH", "chains": []string{"Proxy"}}
		}
		body, _ := json.Marshal(map[string]any{"connections": connections})
		at := base + int64(burst)*6*60000
		if err = s.ApplyFrame(ctx, frame(t, string(body), at), burst > 0); err != nil {
			t.Fatal(err)
		}
		if err = s.ApplyFrame(ctx, frame(t, `{"connections":[]}`, at+1000), true); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Project(ctx, base+3600000); err != nil {
		t.Fatal(err)
	}
	if err = s.Analyze(ctx, base+3600000); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE problem_records SET latest_evidence_json='{}'"); err != nil {
		t.Fatal(err)
	}
	if err = s.Cleanup(ctx, time.Now().UnixMilli(), 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	detail, err := s.Detail(ctx, "domain", "history.example")
	if err != nil {
		t.Fatal(err)
	}
	history := detail["problem_history"].([]map[string]any)
	if len(history) == 0 {
		t.Fatal("historical occurrence missing after raw cleanup")
	}
	ev := history[0]["evidence"].(map[string]any)
	if _, ok := ev["bursts"]; !ok {
		t.Fatalf("old evidence was replaced by latest summary: %v", ev)
	}
}

func TestDroppedFrameReasonAppearsOnDashboard(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "drops.db"), "UTC")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UnixMilli()
	if err = s.RecordGap(context.Background(), "connections", "mailbox_overflow", now-1000, now, 3); err != nil {
		t.Fatal(err)
	}
	dashboard, err := s.Dashboard(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	gaps := dashboard["recent_gaps"].([]map[string]any)
	if len(gaps) != 1 || gaps[0]["reason"] != "mailbox_overflow" || gaps[0]["dropped_samples"] != int64(3) {
		t.Fatalf("drop not visible: %v", gaps)
	}
}

func TestDomainDetailShowsBothSourcesAndObservedPaths(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "sources.db"), "UTC")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Hour).UnixMilli() + 1000
	body := `{"connections":[
	{"id":"host","metadata":{"host":"same.example","destinationIP":"192.0.2.1"},"rule":"DIRECT","chains":["DIRECT"]},
	{"id":"sniff","metadata":{"sniffHost":"same.example","destinationIP":"192.0.2.2"},"rule":"MATCH","chains":["Proxy"]}
	]}`
	if err = s.ApplyFrame(ctx, frame(t, body, at), false); err != nil {
		t.Fatal(err)
	}
	if err = s.Project(ctx, at+1000); err != nil {
		t.Fatal(err)
	}
	detail, err := s.Detail(ctx, "domain", "same.example")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail["observed_sources"].([]map[string]any)) != 2 || len(detail["paths"].([]map[string]any)) != 2 {
		t.Fatalf("source or path missing: %v", detail)
	}
}

func TestGapDoesNotCountUnseenBytesOrMarkAbsent(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "test.db"), "UTC")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Hour).UnixMilli() + 1000
	initial := `{"connections":[{"id":"a","download":5,"metadata":{"destinationIP":"1.1.1.1"}}]}`
	if e = s.ApplyFrame(ctx, frame(t, initial, base), false); e != nil {
		t.Fatal(e)
	}
	if e = s.ApplyFrame(ctx, frame(t, `{"connections":null}`, base+10000), false); e != nil {
		t.Fatal(e)
	}
	var state string
	e = s.db.QueryRow("SELECT observation_state FROM connections_raw").Scan(&state)
	if e != nil || state != "unknown" {
		t.Fatalf("state=%s err=%v", state, e)
	}
	var down int64
	e = s.db.QueryRow("SELECT COALESCE(SUM(observed_download_bytes),0) FROM connection_hours").Scan(&down)
	if e != nil || down != 0 {
		t.Fatalf("down=%d err=%v", down, e)
	}
}
func TestResumedConnectionUsesNewBaseline(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "test.db"), "UTC")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Hour).UnixMilli() + 1000
	a := `{"connections":[{"id":"a","download":5,"metadata":{"host":"a.example.com","destinationIP":"192.0.2.1"}}]}`
	b := `{"connections":[{"id":"a","download":5000,"metadata":{"host":"a.example.com","destinationIP":"192.0.2.1"}}]}`
	c := `{"connections":[{"id":"a","download":5100,"metadata":{"host":"a.example.com","destinationIP":"192.0.2.1"}}]}`
	for i, x := range []struct {
		body       string
		at         int64
		continuous bool
	}{{a, base, false}, {b, base + 10000, false}, {c, base + 11000, true}} {
		if e = s.ApplyFrame(ctx, frame(t, x.body, x.at), x.continuous); e != nil {
			t.Fatalf("frame %d: %v", i, e)
		}
	}
	var down int64
	e = s.db.QueryRow("SELECT COALESCE(SUM(observed_download_bytes),0) FROM connection_hours").Scan(&down)
	if e != nil || down != 100 {
		t.Fatalf("down=%d err=%v", down, e)
	}
}

func TestRepeatedConnectionEvidenceNeedsTwoBursts(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "test.db"), "UTC")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour).UnixMilli() + 60000
	for burst := 0; burst < 2; burst++ {
		cs := make([]map[string]any, 12)
		for i := range cs {
			cs[i] = map[string]any{"id": fmt.Sprintf("%d-%d", burst, i), "download": 0, "metadata": map[string]any{"host": "cdn.example.com", "destinationIP": "203.0.113.1", "destinationPort": "443", "network": "tcp"}}
		}
		b, _ := json.Marshal(map[string]any{"connections": cs})
		at := base + int64(burst)*6*60000
		if e = s.ApplyFrame(ctx, frame(t, string(b), at), burst > 0); e != nil {
			t.Fatal(e)
		}
		if e = s.ApplyFrame(ctx, frame(t, `{"connections":null}`, at+1000), true); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.Analyze(ctx, base+3600000); e != nil {
		t.Fatal(e)
	}
	var n int
	e = s.db.QueryRow("SELECT COUNT(*) FROM problem_records WHERE problem_kind='repeated_connections'").Scan(&n)
	if e != nil || n != 1 {
		t.Fatalf("count=%d err=%v", n, e)
	}
	if e = s.Project(ctx, base+3600000); e != nil {
		t.Fatal(e)
	}
	var routeID int64
	if e = s.db.QueryRow("SELECT route_id FROM problem_records WHERE problem_kind='repeated_connections'").Scan(&routeID); e != nil {
		t.Fatal(e)
	}
	pathDetail, e := s.Detail(ctx, "proxy_path", fmt.Sprint(routeID))
	if e != nil || len(pathDetail["related_problems"].([]map[string]any)) != 1 {
		t.Fatalf("path evidence missing: %v %v", pathDetail, e)
	}
	if _, e = s.db.Exec("UPDATE connections_raw SET target_value='other.example.com' WHERE target_value='cdn.example.com'"); e != nil {
		t.Fatal(e)
	}
	if e = s.Analyze(ctx, base+3600000); e != nil {
		t.Fatal(e)
	}
	e = s.db.QueryRow("SELECT COUNT(*) FROM problem_records WHERE target_value='cdn.example.com'").Scan(&n)
	if e != nil || n != 0 {
		t.Fatalf("stale problems=%d err=%v", n, e)
	}
}

func TestActiveDurationSplitsAtHour(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "test.db"), "UTC")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	end := time.Now().UTC().Truncate(time.Hour).UnixMilli()
	body := `{"connections":[{"id":"cross","download":4,"metadata":{"host":"cross.example.com","destinationIP":"192.0.2.1"}}]}`
	if e = s.ApplyFrame(ctx, frame(t, body, end-1000), false); e != nil {
		t.Fatal(e)
	}
	if e = s.ApplyFrame(ctx, frame(t, body, end+1000), true); e != nil {
		t.Fatal(e)
	}
	rows, e := s.db.Query("SELECT bucket_start_ms,observed_active_ms FROM connection_hours ORDER BY bucket_start_ms")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	var buckets, active []int64
	for rows.Next() {
		var b, a int64
		if e = rows.Scan(&b, &a); e != nil {
			t.Fatal(e)
		}
		buckets = append(buckets, b)
		active = append(active, a)
	}
	if len(active) != 2 || active[0] != 1000 || active[1] != 1000 {
		t.Fatalf("buckets=%v active=%v", buckets, active)
	}
}

func TestIPv6DifferenceUsesSamePathAndAdequateFamilies(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "test.db"), "UTC")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	yesterday := time.Now().UTC().Truncate(24 * time.Hour).Add(-24 * time.Hour).UnixMilli()
	seq := 0
	write := func(at int64, ip string, n int) {
		t.Helper()
		cs := make([]map[string]any, n)
		for i := range cs {
			seq++
			cs[i] = map[string]any{"id": fmt.Sprintf("c%d", seq), "metadata": map[string]any{"host": "dual.example.com", "destinationIP": ip, "destinationPort": "443", "network": "tcp"}}
		}
		b, _ := json.Marshal(map[string]any{"connections": cs})
		if e := s.ApplyFrame(ctx, frame(t, string(b), at), seq != n); e != nil {
			t.Fatal(e)
		}
		if e := s.ApplyFrame(ctx, frame(t, `{"connections":null}`, at+1000), true); e != nil {
			t.Fatal(e)
		}
	}
	for h := int64(0); h < 2; h++ {
		at := yesterday + int64(1+h)*3600000
		write(at, "2001:db8::1", 12)
		write(at+120000, "192.0.2.1", 6)
		write(at+180000, "192.0.2.1", 6)
	}
	if e = s.Analyze(ctx, time.Now().UnixMilli()); e != nil {
		t.Fatal(e)
	}
	var n int
	e = s.db.QueryRow("SELECT COUNT(*) FROM problem_records WHERE problem_kind='ipv6_observation_difference'").Scan(&n)
	if e != nil || n != 1 {
		t.Fatalf("count=%d err=%v", n, e)
	}
}
