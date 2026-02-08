package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/conops/conops/internal/api"
	"github.com/go-chi/chi/v5"
)

type registerAppRequest struct {
	Name           string `json:"name"`
	RepoURL        string `json:"repo_url"`
	RepoAuthMethod string `json:"repo_auth_method"`
	DeployKey      string `json:"deploy_key"`
	Branch         string `json:"branch"`
	ComposePath    string `json:"compose_path"`
	PollInterval   string `json:"poll_interval"`
}

// RuntimeCleaner performs best-effort runtime cleanup for an app.
type RuntimeCleaner interface {
	Destroy(ctx context.Context, appID, composePath string, envVars map[string]string) (string, error)
}

// RuntimeApplier applies desired app state to the runtime.
type RuntimeApplier interface {
	Apply(
		ctx context.Context,
		appID, content string,
		envVars map[string]string,
		repoURL, branch, composePath, commitHash string,
		deployKey []byte,
		onProgress func(string),
	) (string, error)
}

// Handler handles HTTP requests for the controller.
type Handler struct {
	Registry *Registry
	Cleaner  RuntimeCleaner
	Applier  RuntimeApplier
	Logger   *slog.Logger
}

// NewHandler creates a new controller handler.
func NewHandler(registry *Registry, cleaner RuntimeCleaner, applier RuntimeApplier, logger *slog.Logger) *Handler {
	return &Handler{
		Registry: registry,
		Cleaner:  cleaner,
		Applier:  applier,
		Logger:   logger,
	}
}

// RegisterApp handles POST /api/v1/apps
func (h *Handler) RegisterApp(w http.ResponseWriter, r *http.Request) {
	var req registerAppRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	app := App{
		Name:           strings.TrimSpace(req.Name),
		RepoURL:        strings.TrimSpace(req.RepoURL),
		RepoAuthMethod: strings.TrimSpace(req.RepoAuthMethod),
		Branch:         strings.TrimSpace(req.Branch),
		ComposePath:    strings.TrimSpace(req.ComposePath),
		PollInterval:   strings.TrimSpace(req.PollInterval),
	}

	if app.Name == "" || app.RepoURL == "" {
		http.Error(w, "App name and repo URL are required", http.StatusBadRequest)
		return
	}

	if err := h.Registry.AddWithDeployKey(&app, req.DeployKey); err != nil {
		status := http.StatusConflict
		errText := strings.ToLower(err.Error())
		if strings.Contains(errText, "required") || strings.Contains(errText, "invalid") || strings.Contains(errText, "unsupported") {
			status = http.StatusBadRequest
		}
		http.Error(w, err.Error(), status)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(api.APIResponse{
		Message: "App registered successfully",
		Data:    app,
	})
}

// ListApps handles GET /api/v1/apps
func (h *Handler) ListApps(w http.ResponseWriter, r *http.Request) {
	apps := h.Registry.List()
	json.NewEncoder(w).Encode(api.APIResponse{
		Data: apps,
	})
}

// GetApp handles GET /api/v1/apps/{id}
func (h *Handler) GetApp(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	app, err := h.Registry.Get(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(api.APIResponse{
		Data: app,
	})
}

// DeleteApp handles DELETE /api/v1/apps/{id}
func (h *Handler) DeleteApp(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	app, err := h.Registry.Get(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	if h.Cleaner != nil {
		cleanupCtx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()

		if _, err := h.Cleaner.Destroy(cleanupCtx, app.ID, app.ComposePath, nil); err != nil {
			if h.Logger != nil {
				h.Logger.Error("Failed to cleanup app runtime", "id", app.ID, "error", err)
			}
			http.Error(w, "failed to cleanup running containers before deletion", http.StatusInternalServerError)
			return
		}
	}

	if err := h.Registry.Delete(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(api.APIResponse{
		Message: "App deleted successfully",
	})
}

// ForceSyncApp handles POST /api/v1/apps/{id}/sync.
func (h *Handler) ForceSyncApp(w http.ResponseWriter, r *http.Request) {
	if h.Applier == nil {
		http.Error(w, "runtime applier is not configured", http.StatusServiceUnavailable)
		return
	}

	id := chi.URLParam(r, "id")
	app, err := h.Registry.Get(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	if app.Status == "syncing" {
		http.Error(w, "sync already in progress", http.StatusConflict)
		return
	}

	syncStartedAt := time.Now()
	if err := h.Registry.UpdateStatus(app.ID, "syncing", &syncStartedAt); err != nil && h.Logger != nil {
		h.Logger.Warn("Failed to mark app syncing", "id", app.ID, "error", err)
	}

	deployKey, err := h.Registry.GetDeployKey(app.ID)
	if err != nil {
		_ = h.Registry.UpdateStatus(app.ID, "error", nil)
		http.Error(w, fmt.Sprintf("failed to load app credentials: %v", err), http.StatusInternalServerError)
		return
	}
	defer zeroBytes(deployKey)

	// Derive from Background so the sync survives reverse-proxy or client
	// disconnects. The reconciler already does this; match that behaviour
	// and honour the same configurable timeout (default 5m, override with
	// CONOPS_SYNC_TIMEOUT).
	syncCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	progress := newSyncProgressReporter(h.Registry, h.Logger, app.ID, syncProgressFlushInterval)
	output, err := h.Applier.Apply(
		syncCtx,
		app.ID,
		"",
		nil,
		app.RepoURL,
		app.Branch,
		app.ComposePath,
		"",
		deployKey,
		progress.Update,
	)
	progress.Flush()
	if err != nil {
		now := time.Now()
		_ = h.Registry.UpdateSyncResult(
			app.ID,
			"error",
			now,
			app.LastSyncedCommit,
			app.LastSyncedCommitMessage,
			output,
			err.Error(),
		)
		if h.Logger != nil {
			h.Logger.Error("Force sync failed", "id", app.ID, "error", err)
		}
		http.Error(w, "force sync failed", http.StatusInternalServerError)
		return
	}

	now := time.Now()
	if err := h.Registry.UpdateSyncResult(
		app.ID,
		"synced",
		now,
		app.LastSeenCommit,
		app.LastSeenCommitMessage,
		output,
		"",
	); err != nil && h.Logger != nil {
		h.Logger.Warn("Failed to update sync status after force sync", "id", app.ID, "error", err)
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(api.APIResponse{
		Message: "App synced successfully",
	})
}
