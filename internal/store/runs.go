package store

import (
	"context"
	"fmt"
)

// Run is one recorded update run.
type Run struct {
	ID         int64  `json:"id"`
	Runner     string `json:"runner"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	Outcome    string `json:"outcome"`
	Report     string `json:"report,omitempty"`
}

// StartRun opens a run record with outcome "running" and returns its id.
func (s *Store) StartRun(ctx context.Context, runner string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		"INSERT INTO runs(runner, started_at, outcome) VALUES(?,?, 'running')",
		runner, iso(nowUTC()))
	if err != nil {
		return 0, fmt.Errorf("start run: %w", err)
	}
	return res.LastInsertId()
}

// FinishRun closes a run record with its outcome and JSON report.
func (s *Store) FinishRun(ctx context.Context, id int64, outcome, report string) error {
	if _, err := s.db.ExecContext(ctx,
		"UPDATE runs SET finished_at=?, outcome=?, report_json=? WHERE id=?",
		iso(nowUTC()), outcome, report, id); err != nil {
		return fmt.Errorf("finish run: %w", err)
	}
	return nil
}

// RecentRuns returns the most recent runs, newest first.
func (s *Store) RecentRuns(ctx context.Context, limit int) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, runner, COALESCE(started_at,''), COALESCE(finished_at,''), outcome, report_json
FROM runs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("recent runs: %w", err)
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.Runner, &r.StartedAt, &r.FinishedAt, &r.Outcome, &r.Report); err != nil {
			return nil, fmt.Errorf("scan run: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recent runs: %w", err)
	}
	return out, nil
}

// SaveRowVerdict records one verified redirect-row verdict for a run
// (written by the drafting phase; the table exists now per the schema).
func (s *Store) SaveRowVerdict(ctx context.Context, runID int64, skill, rowJSON, checksJSON, outcome, quote, snapshotSHA string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO row_verdicts(run_id, skill, row_json, checks_json, outcome, quote, snapshot_sha)
VALUES(?,?,?,?,?,?,?)`,
		runID, skill, rowJSON, checksJSON, outcome, quote, snapshotSHA)
	if err != nil {
		return fmt.Errorf("save row verdict: %w", err)
	}
	return nil
}
