package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/conops/conops/pkg/api"
	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore initializes the SQLite database and creates necessary tables.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Create apps table
	query := `
	CREATE TABLE IF NOT EXISTS apps (
		id TEXT PRIMARY KEY,
		name TEXT,
		repo_url TEXT,
		branch TEXT,
		compose_path TEXT,
		poll_interval TEXT,
		last_seen_commit TEXT,
		last_sync_at DATETIME,
		status TEXT
	);
	`
	if _, err := db.Exec(query); err != nil {
		return nil, fmt.Errorf("failed to create apps table: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) CreateApp(ctx context.Context, app *api.App) error {
	query := `
	INSERT INTO apps (id, name, repo_url, branch, compose_path, poll_interval, last_seen_commit, last_sync_at, status)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.ExecContext(ctx, query, app.ID, app.Name, app.RepoURL, app.Branch, app.ComposePath, app.PollInterval, app.LastSeenCommit, app.LastSyncAt, app.Status)
	return err
}

func (s *SQLiteStore) GetApp(ctx context.Context, id string) (*api.App, error) {
	query := `SELECT id, name, repo_url, branch, compose_path, poll_interval, last_seen_commit, last_sync_at, status FROM apps WHERE id = ?`
	row := s.db.QueryRowContext(ctx, query, id)

	var app api.App
	err := row.Scan(&app.ID, &app.Name, &app.RepoURL, &app.Branch, &app.ComposePath, &app.PollInterval, &app.LastSeenCommit, &app.LastSyncAt, &app.Status)
	if err != nil {
		return nil, err
	}
	return &app, nil
}

func (s *SQLiteStore) ListApps(ctx context.Context) ([]*api.App, error) {
	query := `SELECT id, name, repo_url, branch, compose_path, poll_interval, last_seen_commit, last_sync_at, status FROM apps`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var apps []*api.App
	for rows.Next() {
		var app api.App
		if err := rows.Scan(&app.ID, &app.Name, &app.RepoURL, &app.Branch, &app.ComposePath, &app.PollInterval, &app.LastSeenCommit, &app.LastSyncAt, &app.Status); err != nil {
			continue
		}
		apps = append(apps, &app)
	}
	return apps, nil
}

func (s *SQLiteStore) DeleteApp(ctx context.Context, id string) error {
	query := `DELETE FROM apps WHERE id = ?`
	result, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("app not found")
	}
	return nil
}

func (s *SQLiteStore) UpdateAppCommit(ctx context.Context, id, commitHash string) error {
	query := `UPDATE apps SET last_seen_commit = ?, last_sync_at = ?, status = ? WHERE id = ?`
	result, err := s.db.ExecContext(ctx, query, commitHash, time.Now(), "active", id)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("app not found")
	}
	return nil
}

func (s *SQLiteStore) Close() {
	s.db.Close()
}
