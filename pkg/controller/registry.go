package controller

import (
	"errors"
	"sync"
	"time"
)

// App represents a Git repository configuration to track.
type App struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	RepoURL        string    `json:"repo_url"`
	Branch         string    `json:"branch"`
	ComposePath    string    `json:"compose_path"`
	PollInterval   string    `json:"poll_interval"` // Duration string e.g. "30s"
	LastSeenCommit string    `json:"last_seen_commit"`
	LastSyncAt     time.Time `json:"last_sync_at"`
	Status         string    `json:"status"` // e.g., "active", "error"
}

// Registry manages the lifecycle of tracked applications.
type Registry struct {
	mu   sync.RWMutex
	apps map[string]*App
}

// NewRegistry creates a new in-memory application registry.
func NewRegistry() *Registry {
	return &Registry{
		apps: make(map[string]*App),
	}
}

// Add registers a new application.
func (r *Registry) Add(app *App) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.apps[app.ID]; exists {
		return errors.New("app already exists")
	}

	// Set defaults if missing
	if app.Branch == "" {
		app.Branch = "main"
	}
	if app.PollInterval == "" {
		app.PollInterval = "30s"
	}
	app.Status = "registered"
	app.LastSyncAt = time.Now()

	r.apps[app.ID] = app
	return nil
}

// Get retrieves an application by ID.
func (r *Registry) Get(id string) (*App, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	app, exists := r.apps[id]
	if !exists {
		return nil, errors.New("app not found")
	}
	return app, nil
}

// List returns all registered applications.
func (r *Registry) List() []*App {
	r.mu.RLock()
	defer r.mu.RUnlock()

	apps := make([]*App, 0, len(r.apps))
	for _, app := range r.apps {
		apps = append(apps, app)
	}
	return apps
}

// Delete removes an application by ID.
func (r *Registry) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.apps[id]; !exists {
		return errors.New("app not found")
	}
	delete(r.apps, id)
	return nil
}

// UpdateCommit updates the latest commit hash for an app.
func (r *Registry) UpdateCommit(id, commitHash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	app, exists := r.apps[id]
	if !exists {
		return errors.New("app not found")
	}

	app.LastSeenCommit = commitHash
	app.LastSyncAt = time.Now()
	app.Status = "active"
	return nil
}
