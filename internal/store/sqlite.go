package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/conops/conops/internal/api"
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

	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	query := `
	CREATE TABLE IF NOT EXISTS apps (
		id TEXT PRIMARY KEY,
		name TEXT,
		repo_url TEXT,
		repo_auth_method TEXT NOT NULL DEFAULT 'public',
		branch TEXT,
		compose_path TEXT,
		poll_interval TEXT,
		last_seen_commit TEXT,
		last_seen_commit_message TEXT,
		last_synced_commit TEXT,
		last_synced_commit_message TEXT,
		last_sync_output TEXT,
		last_sync_error TEXT,
		last_sync_at DATETIME,
		status TEXT
	);
	`
	if _, err := db.Exec(query); err != nil {
		return nil, fmt.Errorf("failed to create apps table: %w", err)
	}

	if err := addSQLiteColumnIfMissing(db, "apps", "repo_auth_method TEXT NOT NULL DEFAULT 'public'"); err != nil {
		return nil, err
	}
	if err := addSQLiteColumnIfMissing(db, "apps", "last_seen_commit_message TEXT NOT NULL DEFAULT ''"); err != nil {
		return nil, err
	}
	if err := addSQLiteColumnIfMissing(db, "apps", "last_synced_commit TEXT NOT NULL DEFAULT ''"); err != nil {
		return nil, err
	}
	if err := addSQLiteColumnIfMissing(db, "apps", "last_synced_commit_message TEXT NOT NULL DEFAULT ''"); err != nil {
		return nil, err
	}
	if err := addSQLiteColumnIfMissing(db, "apps", "last_sync_output TEXT NOT NULL DEFAULT ''"); err != nil {
		return nil, err
	}
	if err := addSQLiteColumnIfMissing(db, "apps", "last_sync_error TEXT NOT NULL DEFAULT ''"); err != nil {
		return nil, err
	}

	credentialsQuery := `
	CREATE TABLE IF NOT EXISTS app_credentials (
		app_id TEXT PRIMARY KEY,
		deploy_key_ciphertext BLOB NOT NULL,
		deploy_key_nonce BLOB NOT NULL,
		FOREIGN KEY(app_id) REFERENCES apps(id) ON DELETE CASCADE
	);
	`
	if _, err := db.Exec(credentialsQuery); err != nil {
		return nil, fmt.Errorf("failed to create app_credentials table: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) CreateApp(ctx context.Context, app *api.App) error {
	query := `
	INSERT INTO apps (
		id,
		name,
		repo_url,
		repo_auth_method,
		branch,
		compose_path,
		poll_interval,
		last_seen_commit,
		last_seen_commit_message,
		last_synced_commit,
		last_synced_commit_message,
		last_sync_output,
		last_sync_error,
		last_sync_at,
		status
	)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.ExecContext(
		ctx,
		query,
		app.ID,
		app.Name,
		app.RepoURL,
		app.RepoAuthMethod,
		app.Branch,
		app.ComposePath,
		app.PollInterval,
		app.LastSeenCommit,
		app.LastSeenCommitMessage,
		app.LastSyncedCommit,
		app.LastSyncedCommitMessage,
		app.LastSyncOutput,
		app.LastSyncError,
		app.LastSyncAt,
		app.Status,
	)
	return err
}

func (s *SQLiteStore) GetApp(ctx context.Context, id string) (*api.App, error) {
	query := `
	SELECT
		id,
		name,
		repo_url,
		repo_auth_method,
		branch,
		compose_path,
		poll_interval,
		COALESCE(last_seen_commit, ''),
		COALESCE(last_seen_commit_message, ''),
		COALESCE(last_synced_commit, ''),
		COALESCE(last_synced_commit_message, ''),
		COALESCE(last_sync_output, ''),
		COALESCE(last_sync_error, ''),
		last_sync_at,
		status
	FROM apps
	WHERE id = ?
	`
	row := s.db.QueryRowContext(ctx, query, id)

	var app api.App
	err := row.Scan(
		&app.ID,
		&app.Name,
		&app.RepoURL,
		&app.RepoAuthMethod,
		&app.Branch,
		&app.ComposePath,
		&app.PollInterval,
		&app.LastSeenCommit,
		&app.LastSeenCommitMessage,
		&app.LastSyncedCommit,
		&app.LastSyncedCommitMessage,
		&app.LastSyncOutput,
		&app.LastSyncError,
		&app.LastSyncAt,
		&app.Status,
	)
	if err != nil {
		return nil, err
	}
	return &app, nil
}

func (s *SQLiteStore) ListApps(ctx context.Context) ([]*api.App, error) {
	query := `
	SELECT
		id,
		name,
		repo_url,
		repo_auth_method,
		branch,
		compose_path,
		poll_interval,
		COALESCE(last_seen_commit, ''),
		COALESCE(last_seen_commit_message, ''),
		COALESCE(last_synced_commit, ''),
		COALESCE(last_synced_commit_message, ''),
		COALESCE(last_sync_output, ''),
		COALESCE(last_sync_error, ''),
		last_sync_at,
		status
	FROM apps
	`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var apps []*api.App
	for rows.Next() {
		var app api.App
		if err := rows.Scan(
			&app.ID,
			&app.Name,
			&app.RepoURL,
			&app.RepoAuthMethod,
			&app.Branch,
			&app.ComposePath,
			&app.PollInterval,
			&app.LastSeenCommit,
			&app.LastSeenCommitMessage,
			&app.LastSyncedCommit,
			&app.LastSyncedCommitMessage,
			&app.LastSyncOutput,
			&app.LastSyncError,
			&app.LastSyncAt,
			&app.Status,
		); err != nil {
			continue
		}
		apps = append(apps, &app)
	}
	return apps, nil
}

func (s *SQLiteStore) DeleteApp(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM app_credentials WHERE app_id = ?`, id); err != nil {
		return err
	}

	query := `DELETE FROM apps WHERE id = ?`
	result, err := tx.ExecContext(ctx, query, id)
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

	return tx.Commit()
}

func (s *SQLiteStore) UpsertAppCredential(ctx context.Context, credential *AppCredential) error {
	query := `
	INSERT INTO app_credentials (app_id, deploy_key_ciphertext, deploy_key_nonce)
	VALUES (?, ?, ?)
	ON CONFLICT(app_id) DO UPDATE SET
		deploy_key_ciphertext = excluded.deploy_key_ciphertext,
		deploy_key_nonce = excluded.deploy_key_nonce
	`
	_, err := s.db.ExecContext(ctx, query, credential.AppID, credential.DeployKeyCiphertext, credential.DeployKeyNonce)
	return err
}

func (s *SQLiteStore) GetAppCredential(ctx context.Context, id string) (*AppCredential, error) {
	query := `SELECT app_id, deploy_key_ciphertext, deploy_key_nonce FROM app_credentials WHERE app_id = ?`
	row := s.db.QueryRowContext(ctx, query, id)

	credential := &AppCredential{}
	if err := row.Scan(&credential.AppID, &credential.DeployKeyCiphertext, &credential.DeployKeyNonce); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrCredentialNotFound
		}
		return nil, err
	}
	return credential, nil
}

func (s *SQLiteStore) DeleteAppCredential(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM app_credentials WHERE app_id = ?`, id)
	return err
}

func (s *SQLiteStore) UpdateAppCommit(ctx context.Context, id, commitHash, commitMessage string) error {
	query := `UPDATE apps SET last_seen_commit = ?, last_seen_commit_message = ?, status = ? WHERE id = ?`
	result, err := s.db.ExecContext(ctx, query, commitHash, commitMessage, "pending", id)
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

func (s *SQLiteStore) UpdateAppSyncResult(
	ctx context.Context,
	id string,
	status string,
	lastSyncAt time.Time,
	syncedCommit string,
	syncedCommitMessage string,
	syncOutput string,
	syncError string,
) error {
	query := `
	UPDATE apps
	SET
		status = ?,
		last_sync_at = ?,
		last_synced_commit = ?,
		last_synced_commit_message = ?,
		last_sync_output = ?,
		last_sync_error = ?
	WHERE id = ?
	`
	result, err := s.db.ExecContext(
		ctx,
		query,
		status,
		lastSyncAt,
		syncedCommit,
		syncedCommitMessage,
		syncOutput,
		syncError,
		id,
	)
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

func (s *SQLiteStore) UpdateAppStatus(ctx context.Context, id, status string, lastSyncAt *time.Time) error {
	if lastSyncAt == nil {
		query := `UPDATE apps SET status = ? WHERE id = ?`
		result, err := s.db.ExecContext(ctx, query, status, id)
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

	query := `UPDATE apps SET status = ?, last_sync_at = ? WHERE id = ?`
	result, err := s.db.ExecContext(ctx, query, status, *lastSyncAt, id)
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

func addSQLiteColumnIfMissing(db *sql.DB, tableName, columnDDL string) error {
	query := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", tableName, columnDDL)
	if _, err := db.Exec(query); err != nil {
		errText := strings.ToLower(err.Error())
		if strings.Contains(errText, "duplicate column name") {
			return nil
		}
		return fmt.Errorf("failed to alter %s table: %w", tableName, err)
	}
	return nil
}
