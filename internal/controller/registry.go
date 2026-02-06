package controller

import (
	"context"
	"time"

	"github.com/conops/conops/internal/api"
	"github.com/conops/conops/internal/store"
)

// App is aliased to api.App for compatibility, though we should prefer api.App.
type App = api.App

// Registry manages the lifecycle of tracked applications using a backend store.
type Registry struct {
	store store.Store
}

// NewRegistry creates a new application registry with the given store backend.
func NewRegistry(s store.Store) *Registry {
	return &Registry{
		store: s,
	}
}

// Add registers a new application.
func (r *Registry) Add(app *api.App) error {
	// Set defaults if missing
	if app.Branch == "" {
		app.Branch = "main"
	}
	if app.PollInterval == "" {
		app.PollInterval = "30s"
	}
	app.Status = "registered"
	app.LastSyncAt = time.Time{}

	return r.store.CreateApp(context.Background(), app)
}

// Get retrieves an application by ID.
func (r *Registry) Get(id string) (*api.App, error) {
	return r.store.GetApp(context.Background(), id)
}

// List returns all registered applications.
func (r *Registry) List() []*api.App {
	apps, err := r.store.ListApps(context.Background())
	if err != nil {
		// Log error? For now return empty list to be safe for UI.
		return []*api.App{}
	}
	return apps
}

// Delete removes an application by ID.
func (r *Registry) Delete(id string) error {
	return r.store.DeleteApp(context.Background(), id)
}

// UpdateCommit updates the latest commit hash for an app.
func (r *Registry) UpdateCommit(id, commitHash string) error {
	return r.store.UpdateAppCommit(context.Background(), id, commitHash)
}

// UpdateStatus updates app status and optionally the last sync time.
func (r *Registry) UpdateStatus(id, status string, lastSyncAt *time.Time) error {
	return r.store.UpdateAppStatus(context.Background(), id, status, lastSyncAt)
}
