package cloudstore

import (
	"fmt"
)

// SystemMetric represents a single system metrics snapshot.
type SystemMetric struct {
	ID         int64   `json:"id"`
	CpuPct     float32 `json:"cpu_pct"`
	MemPct     float32 `json:"mem_pct"`
	DiskPct    float32 `json:"disk_pct"`
	Load1m     float32 `json:"load_1m"`
	MemUsedMB  int     `json:"mem_used_mb"`
	MemTotalMB int     `json:"mem_total_mb"`
	RecordedAt string  `json:"recorded_at"`
}

// RecordMetric inserts a system metrics snapshot.
func (cs *CloudStore) RecordMetric(cpuPct, memPct, diskPct, load1m float32, memUsedMB, memTotalMB int) error {
	_, err := cs.db.Exec(
		`INSERT INTO system_metrics (cpu_pct, mem_pct, disk_pct, load_1m, mem_used_mb, mem_total_mb)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		cpuPct, memPct, diskPct, load1m, memUsedMB, memTotalMB,
	)
	if err != nil {
		return fmt.Errorf("cloudstore: record metric: %w", err)
	}
	return nil
}

// RecentMetrics returns the last N metrics snapshots.
func (cs *CloudStore) RecentMetrics(limit int) ([]SystemMetric, error) {
	if limit <= 0 {
		limit = 60
	}
	rows, err := cs.db.Query(
		`SELECT id, cpu_pct, mem_pct, disk_pct, load_1m, mem_used_mb, mem_total_mb, recorded_at
		 FROM system_metrics
		 ORDER BY recorded_at DESC
		 LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: recent metrics: %w", err)
	}
	defer rows.Close()

	var metrics []SystemMetric
	for rows.Next() {
		var m SystemMetric
		if err := rows.Scan(&m.ID, &m.CpuPct, &m.MemPct, &m.DiskPct, &m.Load1m,
			&m.MemUsedMB, &m.MemTotalMB, &m.RecordedAt); err != nil {
			return nil, fmt.Errorf("cloudstore: scan metric: %w", err)
		}
		metrics = append(metrics, m)
	}
	return metrics, nil
}

// PruneMetrics deletes metrics older than the given hours.
func (cs *CloudStore) PruneMetrics(olderThanHours int) error {
	_, err := cs.db.Exec(
		`DELETE FROM system_metrics WHERE recorded_at < NOW() - INTERVAL '1 hour' * $1`,
		olderThanHours,
	)
	return err
}
