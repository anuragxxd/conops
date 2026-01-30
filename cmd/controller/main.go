package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/conops/conops/pkg/api"
	"github.com/conops/conops/pkg/controller"
	"github.com/conops/conops/pkg/ui"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type AgentState struct {
	Registration *api.AgentRegistration
	LastSeen     time.Time
	LastStatus   string
}

type Server struct {
	mu        sync.RWMutex
	agents    map[string]*AgentState
	registry  *controller.Registry
	taskQueue *controller.TaskQueue
	logger    *slog.Logger
}

func NewServer() *Server {
	return &Server{
		agents:    make(map[string]*AgentState),
		registry:  controller.NewRegistry(),
		taskQueue: controller.NewTaskQueue(),
		logger:    slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req api.AgentRegistration
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		http.Error(w, "Agent ID is required", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.agents[req.ID] = &AgentState{
		Registration: &req,
		LastSeen:     time.Now(),
		LastStatus:   "registered",
	}
	s.mu.Unlock()

	s.logger.Info("New agent registered", "id", req.ID, "hostname", req.Hostname)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(api.APIResponse{
		Message: "Registration successful",
	})
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req api.AgentHeartbeat
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	agent, exists := s.agents[req.ID]
	if !exists {
		s.mu.Unlock()
		http.Error(w, "Agent not registered", http.StatusUnauthorized)
		return
	}

	agent.LastSeen = time.Now()
	agent.LastStatus = req.Status
	s.mu.Unlock()

	s.logger.Debug("Heartbeat received", "id", req.ID, "status", req.Status)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(api.APIResponse{
		Message: "Heartbeat acknowledged",
	})
}

func main() {
	server := NewServer()

	// Start Git Watcher
	watcher := controller.NewGitWatcher(server.registry, server.taskQueue, server.logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watcher.Start(ctx)

	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	appHandler := controller.NewHandler(server.registry, server.taskQueue, server.logger)
	uiHandler, err := ui.NewHandler(server.registry, "web/templates")
	if err != nil {
		server.logger.Error("Failed to initialize UI handler", "error", err)
		os.Exit(1)
	}

	// UI Routes
	r.Route("/ui", func(r chi.Router) {
		r.Get("/apps", uiHandler.ServeAppsPage)
		r.Get("/apps/fragment", uiHandler.ServeAppsFragment)
		r.Post("/apps/add", uiHandler.HandleAddApp)
		r.Handle("/static/*", http.StripPrefix("/ui/static/", http.FileServer(http.Dir("web/static"))))
	})

	// Redirect root to UI
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/apps", http.StatusFound)
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/agent", func(r chi.Router) {
			r.Post("/register", server.handleRegister)
			r.Post("/heartbeat", server.handleHeartbeat)
		})
		r.Route("/apps", func(r chi.Router) {
			r.Post("/", appHandler.RegisterApp)
			r.Get("/", appHandler.ListApps)
			r.Get("/{id}", appHandler.GetApp)
			r.Delete("/{id}", appHandler.DeleteApp)
		})
		r.Route("/tasks", func(r chi.Router) {
			r.Get("/next", appHandler.GetNextTask)
			r.Post("/result", appHandler.SubmitTaskResult)
		})
	})

	addr := ":8080"
	server.logger.Info("Starting controller", "addr", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		server.logger.Error("Server failed", "error", err)
		os.Exit(1)
	}
}
