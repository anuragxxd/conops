package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/conops/conops/internal/compose"
	"github.com/conops/conops/internal/controller"
	"github.com/conops/conops/internal/credentials"
	"github.com/conops/conops/internal/store"
	"github.com/conops/conops/internal/ui"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	dataDir := "/data"
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		// Fallback for local development if /data doesn't exist
		dataDir = "."
	}

	var dbStore store.Store
	var err error

	dbType := os.Getenv("DB_TYPE")
	if dbType == "postgres" {
		connString := os.Getenv("DB_CONNECTION_STRING")
		if connString == "" {
			logger.Error("DB_CONNECTION_STRING is required for postgres")
			os.Exit(1)
		}
		dbStore, err = store.NewPostgresStore(context.Background(), connString)
		if err != nil {
			logger.Error("Failed to initialize postgres store", "error", err)
			os.Exit(1)
		}
		logger.Info("Using PostgreSQL store")
	} else {
		// Default to SQLite
		dbPath := filepath.Join(dataDir, "conops.db")

		dbStore, err = store.NewSQLiteStore(dbPath)
		if err != nil {
			logger.Error("Failed to initialize sqlite store", "error", err)
			os.Exit(1)
		}
		logger.Info("Using SQLite store")
	}
	defer dbStore.Close()

	credentialService, err := credentials.NewServiceFromEnv(filepath.Join(dataDir, "conops-encryption.key"))
	if err != nil {
		logger.Error("Failed to initialize credential encryption", "error", err)
		os.Exit(1)
	}
	logger.Info("Credential encryption is enabled", "source", credentialService.KeySource())

	registry := controller.NewRegistry(dbStore, credentialService)

	// Start Git Watcher
	watcher := controller.NewGitWatcher(registry, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watcher.Start(ctx)

	reconcilerCfg, err := controller.LoadReconcilerConfigFromEnv()
	if err != nil {
		logger.Error("Failed to load reconciler config", "error", err)
		os.Exit(1)
	}
	executor := compose.NewComposeExecutor(logger)
	reconciler := controller.NewReconciler(registry, executor, logger, reconcilerCfg)
	go reconciler.Run(ctx)

	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	appHandler := controller.NewHandler(registry, executor, executor, logger)
	uiHandler, err := ui.NewHandler(registry, executor, "web/templates")
	if err != nil {
		logger.Error("Failed to initialize UI handler", "error", err)
		os.Exit(1)
	}

	// UI Routes
	r.Route("/ui", func(r chi.Router) {
		r.Get("/apps", uiHandler.ServeAppsPage)
		r.Get("/apps/new", uiHandler.ServeNewAppPage)
		r.Get("/apps/fragment", uiHandler.ServeAppsFragment)
		r.Get("/apps/{id}", uiHandler.ServeAppDetailPage)
		r.Post("/apps", uiHandler.HandleAddApp)
		r.Post("/apps/add", uiHandler.HandleAddApp)
		r.Handle("/static/*", http.StripPrefix("/ui/static/", http.FileServer(http.Dir("web/static"))))
	})

	// Redirect root to UI
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/apps", http.StatusFound)
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/apps", func(r chi.Router) {
			r.Post("/", appHandler.RegisterApp)
			r.Get("/", appHandler.ListApps)
			r.Get("/{id}", appHandler.GetApp)
			r.Post("/{id}/sync", appHandler.ForceSyncApp)
			r.Delete("/{id}", appHandler.DeleteApp)
		})
	})

	addr := ":8080"
	logger.Info("Starting controller", "addr", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		logger.Error("Server failed", "error", err)
		os.Exit(1)
	}
}
