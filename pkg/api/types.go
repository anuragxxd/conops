package api

import "time"

// App represents a Git repository configuration to track.
// Moved from controller/registry.go to avoid circular dependency with store package.
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

// AgentRegistration represents the payload sent by an agent to register itself.
type AgentRegistration struct {
	ID           string   `json:"id"`
	Hostname     string   `json:"hostname"`
	Capabilities []string `json:"capabilities"`
}

// AgentHeartbeat represents the periodic status update sent by the agent.
type AgentHeartbeat struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Status    string    `json:"status"` // e.g., "healthy", "degraded"
}

// SyncTask represents a desired state configuration to be applied by the agent.
type SyncTask struct {
	ID             string            `json:"id"`
	ComposeContent string            `json:"compose_content"`
	EnvVars        map[string]string `json:"env_vars"`
	RepoURL        string            `json:"repo_url"`
	Branch         string            `json:"branch"`
	ComposePath    string            `json:"compose_path"`
}

// SyncResult represents the outcome of a sync operation.
type SyncResult struct {
	TaskID    string    `json:"task_id"`
	Success   bool      `json:"success"`
	Logs      string    `json:"logs"`
	Timestamp time.Time `json:"timestamp"`
}

// APIResponse is a standard wrapper for API responses.
type APIResponse struct {
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}
