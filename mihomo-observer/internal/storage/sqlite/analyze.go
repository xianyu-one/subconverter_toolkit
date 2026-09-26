package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

type signalRow struct {
	kind, value, source           string
	route                         int64
	ip                            string
	port                          int
	network, state                string
	first, last, download, active int64
	samples                       int
	family                        int
}
type signalKey struct {
	kind, value string
	route       int64
	ip          string
	port        int
	network     string
	bucket      int64
	source      string
}
type familyKey struct {
	domain, source string
	route          int64
	port           int
	network        string
}
type familyEvidence struct {
	total, suspect int
	hours          map[int64]bool
	first, last    int64
}

func (s *Store) Analyze(ctx context.Context, now int64) error {
	// Recompute recent evidence from observed connections. No request outcome is inferred.
	weekStart := time.UnixMilli(s.day(now)).In(s.zone).AddDate(0, 0, -7).UnixMilli()
	if _, err := s.db.ExecContext(ctx, "CREATE TEMP TABLE IF NOT EXISTS analysis_seen(fingerprint TEXT NOT NULL,window_start_ms INTEGER NOT NULL,window_end_ms INTEGER NOT NULL,PRIMARY KEY(fingerprint,window_start_ms,window_end_ms))"); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM analysis_seen"); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT c.target_kind,COALESCE(c.target_value,''),c.target_source,c.route_id,COALESCE(c.destination_ip,''),COALESCE(c.destination_port,0),c.network,c.observation_state,c.first_seen_at_ms,c.last_seen_at_ms,c.download_bytes,COALESCE((SELECT SUM(observed_active_ms) FROM connection_hours ch WHERE ch.connection_id=c.id),0),c.sample_count,c.ip_version FROM connections_raw c WHERE c.first_seen_at_ms>=? AND c.first_seen_at_ms<? AND c.target_kind<>'unknown' AND NOT EXISTS(SELECT 1 FROM collection_gaps g WHERE g.stream='connections' AND g.started_at_ms>c.first_seen_at_ms AND g.started_at_ms<c.last_seen_at_ms) ORDER BY c.first_seen_at_ms LIMIT 100001`, weekStart, hour(now))
	if err != nil {
		return err
	}
	groups := map[signalKey][]signalRow{}
	rowCount := 0
	for rows.Next() {
		rowCount++
		if rowCount > 100000 {
			rows.Close()
			return fmt.Errorf("analysis window too large")
		}
		var r signalRow
		if err = rows.Scan(&r.kind, &r.value, &r.source, &r.route, &r.ip, &r.port, &r.network, &r.state, &r.first, &r.last, &r.download, &r.active, &r.samples, &r.family); err != nil {
			rows.Close()
			return err
		}
		k := signalKey{kind: r.kind, value: r.value, route: r.route, ip: r.ip, port: r.port, network: r.network, bucket: hour(r.first)}
		groups[k] = append(groups[k], r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	families := map[familyKey]map[int]*familyEvidence{}
	for k, rs := range groups {
		minuteCounts := map[int64]int{}
		short := 0
		smallLong := 0
		for _, r := range rs {
			minuteCounts[r.first/60000]++
			if r.state == "absent" && r.active < 5000 {
				short++
			}
			if r.state == "absent" && r.last < k.bucket+3600000 && r.active >= 10000 && r.samples >= 5 && r.download <= 8192 && r.network == "tcp" {
				smallLong++
			}
		}
		minutes := make([]int64, 0, len(minuteCounts))
		for m := range minuteCounts {
			minutes = append(minutes, m)
		}
		sort.Slice(minutes, func(i, j int) bool { return minutes[i] < minutes[j] })
		bursts := 0
		lastBurst := int64(-1000000)
		for _, m := range minutes {
			if minuteCounts[m] >= 12 && m-lastBurst >= 5 {
				bursts++
				lastBurst = m
			}
		}
		firstEvidence, lastEvidence := rs[0].first, rs[0].first
		for _, r := range rs {
			if r.first < firstEvidence {
				firstEvidence = r.first
			}
			if r.first > lastEvidence {
				lastEvidence = r.first
			}
		}
		if bursts >= 2 {
			ev := map[string]any{"observed_connections": len(rs), "bursts": bursts, "short_connections": short, "window": "hour", "interpretation": "repeated observed connections; request retries or failures are unverified"}
			if err = s.saveProblem(ctx, k, "repeated_connections", 1, 1, len(rs), k.bucket, k.bucket+3600000, firstEvidence, lastEvidence, ev); err != nil {
				return err
			}
		}
		if smallLong >= 3 && bursts >= 2 {
			ev := map[string]any{"small_long_connections": smallLong, "observed_connections": len(rs), "burst_evidence": bursts, "duration_threshold_ms": 10000, "download_threshold_bytes": 8192, "interpretation": "small cumulative downloads on long observed TCP connections; application protocol is unknown"}
			if err = s.saveProblem(ctx, k, "small_long_connections", 2, 1, smallLong, k.bucket, k.bucket+3600000, firstEvidence, lastEvidence, ev); err != nil {
				return err
			}
		}
		for _, r := range rs {
			if r.kind != "domain" || r.ip == "" || (r.family != 4 && r.family != 6) || r.state != "absent" || r.first < weekStart || r.last >= s.day(now) {
				continue
			}
			fk := familyKey{r.value, r.source, r.route, r.port, r.network}
			if families[fk] == nil {
				families[fk] = map[int]*familyEvidence{}
			}
			fe := families[fk][r.family]
			if fe == nil {
				fe = &familyEvidence{hours: map[int64]bool{}}
				families[fk][r.family] = fe
			}
			fe.total++
			fe.hours[hour(r.first)] = true
			if fe.first == 0 || r.first < fe.first {
				fe.first = r.first
			}
			if r.last > fe.last {
				fe.last = r.last
			}
			if minuteCounts[r.first/60000] >= 12 || (r.active >= 10000 && r.samples >= 5 && r.download <= 8192 && r.network == "tcp") {
				fe.suspect++
			}
		}
	}
	for fk, fams := range families {
		v4, v6 := fams[4], fams[6]
		if v4 == nil || v6 == nil || v4.total < 20 || v6.total < 20 || len(v4.hours) < 2 || len(v6.hours) < 2 {
			continue
		}
		r4 := float64(v4.suspect) / float64(v4.total)
		r6 := float64(v6.suspect) / float64(v6.total)
		if r6 < 0.30 || r6-r4 < 0.20 || (v4.suspect > 0 && r6 < 3*r4) {
			continue
		}
		end := s.day(now)
		start := time.UnixMilli(end).In(s.zone).AddDate(0, 0, -7).UnixMilli()
		ev := map[string]any{"domain_source": fk.source, "ipv4_eligible": v4.total, "ipv4_suspect": v4.suspect, "ipv4_observed_hours": len(v4.hours), "ipv6_eligible": v6.total, "ipv6_suspect": v6.suspect, "ipv6_observed_hours": len(v6.hours), "interpretation": "IPv6 has more repeated or small-long connection evidence; this is not a failure rate or proof of fallback"}
		k := signalKey{kind: "domain", value: fk.domain, route: fk.route, port: fk.port, network: fk.network, source: fk.source}
		first, last := v4.first, v4.last
		if v6.first < first {
			first = v6.first
		}
		if v6.last > last {
			last = v6.last
		}
		if err = s.saveProblem(ctx, k, "ipv6_observation_difference", 2, 1, v6.total, start, end, first, last, ev); err != nil {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `DELETE FROM problem_occurrences WHERE window_start_ms>=? AND window_end_ms<=? AND NOT EXISTS(SELECT 1 FROM analysis_seen a JOIN problem_records p ON p.fingerprint=a.fingerprint WHERE p.id=problem_occurrences.problem_id AND a.window_start_ms=problem_occurrences.window_start_ms AND a.window_end_ms=problem_occurrences.window_end_ms)`, weekStart, hour(now))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM problem_records WHERE NOT EXISTS(SELECT 1 FROM problem_occurrences o WHERE o.problem_id=problem_records.id)")
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE problem_records SET first_evidence_at_ms=(SELECT MIN(evidence_first_seen_ms) FROM problem_occurrences o WHERE o.problem_id=problem_records.id),last_evidence_at_ms=(SELECT MAX(evidence_last_seen_ms) FROM problem_occurrences o WHERE o.problem_id=problem_records.id),severity=(SELECT severity FROM problem_occurrences o WHERE o.problem_id=problem_records.id ORDER BY evidence_last_seen_ms DESC LIMIT 1),confidence=(SELECT confidence FROM problem_occurrences o WHERE o.problem_id=problem_records.id ORDER BY evidence_last_seen_ms DESC LIMIT 1),latest_sample_count=(SELECT sample_count FROM problem_occurrences o WHERE o.problem_id=problem_records.id ORDER BY evidence_last_seen_ms DESC LIMIT 1),latest_evidence_json=(SELECT evidence_json FROM problem_occurrences o WHERE o.problem_id=problem_records.id ORDER BY evidence_last_seen_ms DESC LIMIT 1)`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) saveProblem(ctx context.Context, k signalKey, kind string, severity, confidence, count int, start, end, evidenceFirst, evidenceLast int64, ev any) error {
	b, _ := json.Marshal(ev)
	fingerprintSource := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s\x00%d\x00%s\x00%s", kind, k.kind, k.value, k.route, k.ip, k.port, k.network, k.source)
	h := sha256.Sum256([]byte(fingerprintSource))
	fp := hex.EncodeToString(h[:])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO problem_records(fingerprint,problem_kind,target_kind,target_value,route_id,destination_ip,destination_port,network,first_evidence_at_ms,last_evidence_at_ms,severity,confidence,latest_sample_count,latest_evidence_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(fingerprint) DO UPDATE SET first_evidence_at_ms=MIN(problem_records.first_evidence_at_ms,excluded.first_evidence_at_ms),last_evidence_at_ms=MAX(problem_records.last_evidence_at_ms,excluded.last_evidence_at_ms),severity=CASE WHEN excluded.last_evidence_at_ms>=problem_records.last_evidence_at_ms THEN excluded.severity ELSE problem_records.severity END,confidence=CASE WHEN excluded.last_evidence_at_ms>=problem_records.last_evidence_at_ms THEN excluded.confidence ELSE problem_records.confidence END,latest_sample_count=CASE WHEN excluded.last_evidence_at_ms>=problem_records.last_evidence_at_ms THEN excluded.latest_sample_count ELSE problem_records.latest_sample_count END,latest_evidence_json=CASE WHEN excluded.last_evidence_at_ms>=problem_records.last_evidence_at_ms THEN excluded.latest_evidence_json ELSE problem_records.latest_evidence_json END`, fp, kind, k.kind, k.value, k.route, nullable(k.ip), nullable(k.port), nullable(k.network), evidenceFirst, evidenceLast, severity, confidence, count, string(b))
	if err != nil {
		return err
	}
	var id int64
	if err = tx.QueryRowContext(ctx, "SELECT id FROM problem_records WHERE fingerprint=?", fp).Scan(&id); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO problem_occurrences(problem_id,window_start_ms,window_end_ms,evidence_first_seen_ms,evidence_last_seen_ms,severity,confidence,sample_count,evidence_json) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(problem_id,window_start_ms,window_end_ms) DO UPDATE SET evidence_first_seen_ms=excluded.evidence_first_seen_ms,evidence_last_seen_ms=excluded.evidence_last_seen_ms,severity=excluded.severity,confidence=excluded.confidence,sample_count=excluded.sample_count,evidence_json=excluded.evidence_json`, id, start, end, evidenceFirst, evidenceLast, severity, confidence, count, string(b))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO analysis_seen(fingerprint,window_start_ms,window_end_ms) VALUES(?,?,?)", fp, start, end)
	if err != nil {
		return err
	}
	return tx.Commit()
}
