package store

import (
	"context"

	"github.com/conops/conops/pkg/api"
)

// Store defines the interface for data persistence.
type Store interface {
	CreateApp(ctx context.Context, app *api.App) error
	GetApp(ctx context.Context, id string) (*api.App, error)
	ListApps(ctx context.Context) ([]*api.App, error)
	DeleteApp(ctx context.Context, id string) error
	UpdateAppCommit(ctx context.Context, id, commitHash string) error
	Close()
}
