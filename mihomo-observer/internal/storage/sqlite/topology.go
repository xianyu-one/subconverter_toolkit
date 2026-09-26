package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"

	"mihomo-observer/internal/storage"
)

func (s *Store) RecordTopology(ctx context.Context, at int64, topology map[string]storage.ProxyInfo) error {
	b, err := json.Marshal(topology)
	if err != nil {
		return err
	}
	h := sha256.Sum256(b)
	fingerprint := hex.EncodeToString(h[:])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous int64
	var lastValid int64
	var oldHash string
	err = tx.QueryRowContext(ctx, "SELECT at_ms,last_valid_ms,fingerprint FROM proxy_topology ORDER BY at_ms DESC LIMIT 1").Scan(&previous, &lastValid, &oldHash)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil && oldHash == fingerprint && at >= lastValid && at-lastValid <= 120000 {
		if _, err = tx.ExecContext(ctx, "UPDATE proxy_topology SET last_valid_ms=? WHERE at_ms=?", at, previous); err != nil {
			return err
		}
	} else {
		if _, err = tx.ExecContext(ctx, "INSERT INTO proxy_topology(at_ms,last_valid_ms,fingerprint,topology_json) VALUES(?,?,?,?)", at, at, fingerprint, string(b)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) TopologyAt(ctx context.Context, at int64) (map[string]storage.ProxyInfo, int64, int64, error) {
	var b string
	var snapshotAt int64
	var valid int64
	err := s.db.QueryRowContext(ctx, "SELECT topology_json,at_ms,last_valid_ms FROM proxy_topology WHERE at_ms<=? ORDER BY at_ms DESC LIMIT 1", at).Scan(&b, &snapshotAt, &valid)
	if err == sql.ErrNoRows {
		return map[string]storage.ProxyInfo{}, 0, 0, nil
	}
	if err != nil {
		return nil, 0, 0, err
	}
	var topology map[string]storage.ProxyInfo
	if err = json.Unmarshal([]byte(b), &topology); err != nil {
		return nil, 0, 0, err
	}
	return topology, snapshotAt, valid, nil
}
