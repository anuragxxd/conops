package api

import "time"

// App represents a Git repository configuration to track.
// Moved from controller/registry.go to avoid circular dependency with store package.
type App struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	RepoURL        string    `json:"repo_url"`
	RepoAuthMethod string    `json:"repo_auth_method"`
	Branch         string    `json:"branch"`
	ComposePath    string    `json:"compose_path"`
	PollInterval   string    `json:"poll_interval"` // Duration string e.g. "30s"
	LastSeenCommit string    `json:"last_seen_commit"`
	LastSyncAt     time.Time `json:"last_sync_at"`
	Status         string    `json:"status"` // e.g., "active", "error"
}

// APIResponse is a standard wrapper for API responses.
type APIResponse struct {
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}
