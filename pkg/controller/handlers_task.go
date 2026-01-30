package controller

import (
	"encoding/json"
	"net/http"

	"github.com/conops/conops/pkg/api"
)

// GetNextTask handles GET /api/v1/tasks/next?app_id=...
func (h *Handler) GetNextTask(w http.ResponseWriter, r *http.Request) {
	appID := r.URL.Query().Get("app_id")
	if appID == "" {
		http.Error(w, "app_id is required", http.StatusBadRequest)
		return
	}
	agentID := r.URL.Query().Get("agent_id")
	if agentID == "" {
		agentID = r.Header.Get("X-Agent-ID")
	}

	task, err := h.TaskQueue.Dequeue(appID)
	if err != nil {
		// Queue empty or app not found
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if h.Logger != nil {
		h.Logger.Info(
			"Task dequeued",
			"app_id", appID,
			"task_id", task.CommitHash,
			"compose_bytes", len(task.ComposeContent),
			"agent_id", agentID,
			"remote", r.RemoteAddr,
		)
	}

	// Convert internal PendingTask to API SyncTask
	apiTask := api.SyncTask{
		ID:             task.CommitHash, // Use commit hash as task ID for now
		ComposeContent: task.ComposeContent,
		EnvVars:        nil, // Phase 4
		RepoURL:        task.RepoURL,
		Branch:         task.Branch,
		ComposePath:    task.ComposePath,
	}

	json.NewEncoder(w).Encode(api.APIResponse{
		Data: apiTask,
	})
}

// SubmitTaskResult handles POST /api/v1/tasks/result
func (h *Handler) SubmitTaskResult(w http.ResponseWriter, r *http.Request) {
	var result api.SyncResult
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	agentID := r.Header.Get("X-Agent-ID")
	if agentID == "" {
		agentID = r.URL.Query().Get("agent_id")
	}
	if result.TaskID == "" {
		if h.Logger != nil {
			h.Logger.Warn("Task result missing task id", "agent_id", agentID, "remote", r.RemoteAddr, "logs_bytes", len(result.Logs))
		}
		http.Error(w, "task_id is required", http.StatusBadRequest)
		return
	}

	if h.Logger != nil {
		h.Logger.Info(
			"Task result received",
			"task_id", result.TaskID,
			"success", result.Success,
			"logs_bytes", len(result.Logs),
			"agent_id", agentID,
			"remote", r.RemoteAddr,
		)
	}

	// Update registry status (simplified for Phase 3)
	// In a real system, we'd match the task ID to the App ID
	// For now, we rely on the agent sending the result.

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(api.APIResponse{
		Message: "Result acknowledged",
	})
}
