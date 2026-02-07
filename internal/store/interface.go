package store

import (
	"context"
	"errors"
	"time"

	"github.com/conops/conops/internal/api"
)

var ErrCredentialNotFound = errors.New("app credential not found")

// Store defines the interface for data persistence.
type Store interface {
	CreateApp(ctx context.Context, app *api.App) error
	GetApp(ctx context.Context, id string) (*api.App, error)
	ListApps(ctx context.Context) ([]*api.App, error)
	DeleteApp(ctx context.Context, id string) error
	UpsertAppCredential(ctx context.Context, credential *AppCredential) error
	GetAppCredential(ctx context.Context, id string) (*AppCredential, error)
	DeleteAppCredential(ctx context.Context, id string) error
	UpdateAppCommit(ctx context.Context, id, commitHash, commitMessage string) error
	UpdateAppStatus(ctx context.Context, id, status string, lastSyncAt *time.Time) error
	UpdateAppSyncResult(
		ctx context.Context,
		id string,
		status string,
		lastSyncAt time.Time,
		syncedCommit string,
		syncedCommitMessage string,
		syncOutput string,
		syncError string,
	) error
	UpdateAppSyncProgress(ctx context.Context, id string, lastSyncAt time.Time, syncOutput string) error
	Close()
}

// AppCredential stores encrypted app-level credentials.
type AppCredential struct {
	AppID               string
	DeployKeyCiphertext []byte
	DeployKeyNonce      []byte
}
