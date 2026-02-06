package store

import (
	"context"
	"time"

	"github.com/conops/conops/internal/api"
)

// Store defines the interface for data persistence.
type Store interface {
	CreateApp(ctx context.Context, app *api.App) error
	GetApp(ctx context.Context, id string) (*api.App, error)
	ListApps(ctx context.Context) ([]*api.App, error)
	DeleteApp(ctx context.Context, id string) error
	UpdateAppCommit(ctx context.Context, id, commitHash string) error
	UpdateAppStatus(ctx context.Context, id, status string, lastSyncAt *time.Time) error
	Close()
}
