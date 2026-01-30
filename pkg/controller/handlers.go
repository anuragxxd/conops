package controller

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/conops/conops/pkg/api"
	"github.com/go-chi/chi/v5"
)

// Handler handles HTTP requests for the controller.
type Handler struct {
	Registry  *Registry
	TaskQueue *TaskQueue
	Logger    *slog.Logger
}

// NewHandler creates a new controller handler.
func NewHandler(registry *Registry, taskQueue *TaskQueue, logger *slog.Logger) *Handler {
	return &Handler{
		Registry:  registry,
		TaskQueue: taskQueue,
		Logger:    logger,
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
	if err := h.Registry.Delete(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(api.APIResponse{
		Message: "App deleted successfully",
	})
}
