package store

import (
	"context"
	"fmt"
	"time"

	"github.com/conops/conops/internal/api"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(ctx context.Context, connString string) (*PostgresStore, error) {
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("unable to parse connection string: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("unable to create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("unable to ping database: %w", err)
	}

	store := &PostgresStore{pool: pool}
	if err := store.migrate(ctx); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return store, nil
}

func (s *PostgresStore) migrate(ctx context.Context) error {
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
		last_seen_commit_message TEXT NOT NULL DEFAULT '',
		last_synced_commit TEXT NOT NULL DEFAULT '',
		last_synced_commit_message TEXT NOT NULL DEFAULT '',
		last_sync_output TEXT NOT NULL DEFAULT '',
		last_sync_error TEXT NOT NULL DEFAULT '',
		last_sync_at TIMESTAMPTZ,
		status TEXT
	);
	`
	if _, err := s.pool.Exec(ctx, query); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `ALTER TABLE apps ADD COLUMN IF NOT EXISTS repo_auth_method TEXT NOT NULL DEFAULT 'public'`); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `ALTER TABLE apps ADD COLUMN IF NOT EXISTS last_seen_commit_message TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `ALTER TABLE apps ADD COLUMN IF NOT EXISTS last_synced_commit TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `ALTER TABLE apps ADD COLUMN IF NOT EXISTS last_synced_commit_message TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `ALTER TABLE apps ADD COLUMN IF NOT EXISTS last_sync_output TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `ALTER TABLE apps ADD COLUMN IF NOT EXISTS last_sync_error TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}

	credentialsQuery := `
	CREATE TABLE IF NOT EXISTS app_credentials (
		app_id TEXT PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
		deploy_key_ciphertext BYTEA,
		deploy_key_nonce BYTEA,
		env_ciphertext BYTEA,
		env_nonce BYTEA
	);
	`
	if _, err := s.pool.Exec(ctx, credentialsQuery); err != nil {
		return err
	}

	// Add env_ciphertext and env_nonce to app_credentials for legacy schemas
	if _, err := s.pool.Exec(ctx, `ALTER TABLE app_credentials ADD COLUMN IF NOT EXISTS env_ciphertext BYTEA`); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `ALTER TABLE app_credentials ADD COLUMN IF NOT EXISTS env_nonce BYTEA`); err != nil {
		return err
	}

	return nil
}

func (s *PostgresStore) CreateApp(ctx context.Context, app *api.App) error {
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
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	`
	_, err := s.pool.Exec(
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

func (s *PostgresStore) GetApp(ctx context.Context, id string) (*api.App, error) {
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
	WHERE id = $1
	`
	row := s.pool.QueryRow(ctx, query, id)

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

func (s *PostgresStore) ListApps(ctx context.Context) ([]*api.App, error) {
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
	rows, err := s.pool.Query(ctx, query)
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

func (s *PostgresStore) DeleteApp(ctx context.Context, id string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM app_credentials WHERE app_id = $1`, id); err != nil {
		return err
	}
	query := `DELETE FROM apps WHERE id = $1`
	ct, err := s.pool.Exec(ctx, query, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("app not found")
	}
	return nil
}

func (s *PostgresStore) UpdateApp(ctx context.Context, id, name, branch, composePath, pollInterval string) error {
	query := `
	UPDATE apps
	SET
		name = $1,
		branch = $2,
		compose_path = $3,
		poll_interval = $4
	WHERE id = $5
	`
	ct, err := s.pool.Exec(ctx, query, name, branch, composePath, pollInterval, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("app not found")
	}
	return nil
}

func (s *PostgresStore) UpsertAppCredential(ctx context.Context, credential *AppCredential) error {
	query := `
	INSERT INTO app_credentials (app_id, deploy_key_ciphertext, deploy_key_nonce, env_ciphertext, env_nonce)
	VALUES ($1, $2, $3, $4, $5)
	ON CONFLICT (app_id) DO UPDATE SET
		deploy_key_ciphertext = CASE WHEN EXCLUDED.deploy_key_ciphertext IS NOT NULL THEN EXCLUDED.deploy_key_ciphertext ELSE app_credentials.deploy_key_ciphertext END,
		deploy_key_nonce = CASE WHEN EXCLUDED.deploy_key_nonce IS NOT NULL THEN EXCLUDED.deploy_key_nonce ELSE app_credentials.deploy_key_nonce END,
		env_ciphertext = CASE WHEN EXCLUDED.env_ciphertext IS NOT NULL THEN EXCLUDED.env_ciphertext ELSE app_credentials.env_ciphertext END,
		env_nonce = CASE WHEN EXCLUDED.env_nonce IS NOT NULL THEN EXCLUDED.env_nonce ELSE app_credentials.env_nonce END
	`
	_, err := s.pool.Exec(ctx, query, credential.AppID, credential.DeployKeyCiphertext, credential.DeployKeyNonce, credential.EnvCiphertext, credential.EnvNonce)
	return err
}

func (s *PostgresStore) GetAppCredential(ctx context.Context, id string) (*AppCredential, error) {
	query := `SELECT app_id, deploy_key_ciphertext, deploy_key_nonce, env_ciphertext, env_nonce FROM app_credentials WHERE app_id = $1`
	row := s.pool.QueryRow(ctx, query, id)

	credential := &AppCredential{}
	var deployKeyCiphertext, deployKeyNonce, envCiphertext, envNonce []byte

	if err := row.Scan(&credential.AppID, &deployKeyCiphertext, &deployKeyNonce, &envCiphertext, &envNonce); err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrCredentialNotFound
		}
		return nil, err
	}
	credential.DeployKeyCiphertext = deployKeyCiphertext
	credential.DeployKeyNonce = deployKeyNonce
	credential.EnvCiphertext = envCiphertext
	credential.EnvNonce = envNonce

	return credential, nil
}

func (s *PostgresStore) DeleteAppCredential(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM app_credentials WHERE app_id = $1`, id)
	return err
}

func (s *PostgresStore) UpdateAppCredentials(ctx context.Context, appID string, envCiphertext, envNonce []byte) error {
	query := `
	UPDATE app_credentials
	SET
		env_ciphertext = $1,
		env_nonce = $2
	WHERE app_id = $3
	`
	ct, err := s.pool.Exec(ctx, query, envCiphertext, envNonce, appID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("app credentials not found")
	}
	return nil
}

func (s *PostgresStore) UpdateAppCommit(ctx context.Context, id, commitHash, commitMessage string) error {
	query := `UPDATE apps SET last_seen_commit = $1, last_seen_commit_message = $2, status = $3 WHERE id = $4`
	ct, err := s.pool.Exec(ctx, query, commitHash, commitMessage, "pending", id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("app not found")
	}
	return nil
}

func (s *PostgresStore) UpdateAppSyncResult(
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
		status = $1,
		last_sync_at = $2,
		last_synced_commit = $3,
		last_synced_commit_message = $4,
		last_sync_output = $5,
		last_sync_error = $6
	WHERE id = $7
	`
	ct, err := s.pool.Exec(
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
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("app not found")
	}
	return nil
}

func (s *PostgresStore) UpdateAppSyncProgress(ctx context.Context, id string, lastSyncAt time.Time, syncOutput string) error {
	query := `
	UPDATE apps
	SET
		status = $1,
		last_sync_at = $2,
		last_sync_output = $3,
		last_sync_error = ''
	WHERE id = $4
	`
	ct, err := s.pool.Exec(ctx, query, "syncing", lastSyncAt, syncOutput, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("app not found")
	}
	return nil
}

func (s *PostgresStore) UpdateAppStatus(ctx context.Context, id, status string, lastSyncAt *time.Time) error {
	if lastSyncAt == nil {
		query := `UPDATE apps SET status = $1 WHERE id = $2`
		ct, err := s.pool.Exec(ctx, query, status, id)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return fmt.Errorf("app not found")
		}
		return nil
	}

	query := `UPDATE apps SET status = $1, last_sync_at = $2 WHERE id = $3`
	ct, err := s.pool.Exec(ctx, query, status, *lastSyncAt, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("app not found")
	}
	return nil
}

func (s *PostgresStore) Close() {
	s.pool.Close()
}
