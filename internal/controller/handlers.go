package controller

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/conops/conops/internal/api"
	"github.com/go-chi/chi/v5"
)

// RuntimeCleaner performs best-effort runtime cleanup for an app.
type RuntimeCleaner interface {
	Destroy(ctx context.Context, appID, composePath string, envVars map[string]string) (string, error)
}

// Handler handles HTTP requests for the controller.
type Handler struct {
	Registry *Registry
	Cleaner  RuntimeCleaner
	Logger   *slog.Logger
}

// NewHandler creates a new controller handler.
func NewHandler(registry *Registry, cleaner RuntimeCleaner, logger *slog.Logger) *Handler {
	return &Handler{
		Registry: registry,
		Cleaner:  cleaner,
		Logger:   logger,
	}
}

// RegisterApp handles POST /api/v1/apps
func (h *Handler) RegisterApp(w http.ResponseWriter, r *http.Request) {
	var app App
	if err := json.NewDecoder(r.Body).Decode(&app); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if app.ID == "" || app.RepoURL == "" {
		http.Error(w, "App ID and Repo URL are required", http.StatusBadRequest)
		return
	}

	if err := h.Registry.Add(&app); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
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
