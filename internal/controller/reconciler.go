package controller

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/conops/conops/internal/compose"
)

// ReconcilerConfig controls how the monolith applies desired state.
type ReconcilerConfig struct {
	Interval    time.Duration
	SyncTimeout time.Duration
	RetryErrors bool
}

// LoadReconcilerConfigFromEnv loads reconciler config from environment variables.
func LoadReconcilerConfigFromEnv() (ReconcilerConfig, error) {
	interval := 10 * time.Second
	if value := strings.TrimSpace(os.Getenv("CONOPS_RECONCILE_INTERVAL")); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return ReconcilerConfig{}, fmt.Errorf("invalid CONOPS_RECONCILE_INTERVAL: %s", value)
		}
		if parsed > 0 {
			interval = parsed
		}
	}

	timeout := 5 * time.Minute
	if value := strings.TrimSpace(os.Getenv("CONOPS_SYNC_TIMEOUT")); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return ReconcilerConfig{}, fmt.Errorf("invalid CONOPS_SYNC_TIMEOUT: %s", value)
		}
		if parsed > 0 {
			timeout = parsed
		}
	}

	retryErrors := strings.EqualFold(os.Getenv("CONOPS_RETRY_ERRORS"), "true")

	return ReconcilerConfig{
		Interval:    interval,
		SyncTimeout: timeout,
		RetryErrors: retryErrors,
	}, nil
}

// Reconciler applies desired state directly on the host (monolith mode).
type Reconciler struct {
	Registry *Registry
	Executor *compose.ComposeExecutor
	Logger   *slog.Logger
	Config   ReconcilerConfig

	mu      sync.Mutex
	running bool
}

// NewReconciler creates a new reconciler.
func NewReconciler(registry *Registry, executor *compose.ComposeExecutor, logger *slog.Logger, cfg ReconcilerConfig) *Reconciler {
	return &Reconciler{
		Registry: registry,
		Executor: executor,
		Logger:   logger,
		Config:   cfg,
	}
}

// Run starts the reconciliation loop.
func (r *Reconciler) Run(ctx context.Context) {
	r.reconcileOnce()

	ticker := time.NewTicker(r.Config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if r.Logger != nil {
				r.Logger.Info("Reconciler stopped")
			}
			return
		case <-ticker.C:
			r.reconcileOnce()
		}
	}
}

func (r *Reconciler) reconcileOnce() {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
	}()

	var runtimeSnapshot map[string]compose.ProjectRuntimeState
	if snapshot, err := r.captureRuntimeSnapshot(); err != nil {
		if r.Logger != nil {
			r.Logger.Warn("Failed to capture runtime snapshot; skipping runtime drift checks", "error", err)
		}
	} else {
		runtimeSnapshot = snapshot
	}

	apps := r.Registry.List()
	for _, app := range apps {
		if app.Status == "syncing" {
			r.requeuePending(app, "recovering_interrupted_sync")
		}

		if app.LastSeenCommit == "" {
			continue
		}

		if app.Status == "synced" && runtimeSnapshot != nil {
			if reason := r.runtimeDriftReason(app.ID, runtimeSnapshot); reason != "" {
				r.requeuePending(app, reason)
			}
		}

		switch app.Status {
		case "pending":
		case "error":
			if !r.Config.RetryErrors {
				continue
			}
		default:
			continue
		}

		if err := r.syncApp(app); err != nil && r.Logger != nil {
			r.Logger.Error("App sync failed", "app_id", app.ID, "error", err)
		}
	}
}

func (r *Reconciler) captureRuntimeSnapshot() (map[string]compose.ProjectRuntimeState, error) {
	if r.Executor == nil {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return r.Executor.SnapshotProjects(ctx)
}

func (r *Reconciler) runtimeDriftReason(appID string, snapshot map[string]compose.ProjectRuntimeState) string {
	projectName := compose.ProjectNameForApp(appID)
	state, ok := snapshot[projectName]
	if !ok || state.ContainerCount == 0 {
		return "runtime_missing"
	}
	if state.UnhealthyCount > 0 {
		return "runtime_unhealthy"
	}
	if state.ExitedCount > 0 {
		return "runtime_exited"
	}
	if state.RunningCount < state.ContainerCount {
		return "runtime_not_running"
	}
	return ""
}

func (r *Reconciler) requeuePending(app *App, reason string) {
	if app.Status == "pending" {
		return
	}
	if err := r.Registry.UpdateStatus(app.ID, "pending", nil); err != nil {
		if r.Logger != nil {
			r.Logger.Warn("Failed to requeue app for reconciliation", "app_id", app.ID, "reason", reason, "error", err)
		}
		return
	}
	app.Status = "pending"
	if r.Logger != nil {
		r.Logger.Info("Requeued app for reconciliation", "app_id", app.ID, "reason", reason)
	}
}

func (r *Reconciler) syncApp(app *App) error {
	if err := r.Registry.UpdateStatus(app.ID, "syncing", nil); err != nil && r.Logger != nil {
		r.Logger.Warn("Failed to mark app syncing", "app_id", app.ID, "error", err)
	}

	deployKey, err := r.Registry.GetDeployKey(app.ID)
	if err != nil {
		_ = r.Registry.UpdateStatus(app.ID, "error", nil)
		return fmt.Errorf("failed to load app credentials: %w", err)
	}
	defer zeroBytes(deployKey)

	ctx, cancel := context.WithTimeout(context.Background(), r.Config.SyncTimeout)
	defer cancel()

	output, err := r.Executor.Apply(ctx, app.ID, "", nil, app.RepoURL, app.Branch, app.ComposePath, app.LastSeenCommit, deployKey)
	if err != nil {
		if r.Logger != nil {
			r.Logger.Error("Sync apply failed", "app_id", app.ID, "commit", app.LastSeenCommit, "output", truncateOutput(output))
		}
		now := time.Now()
		_ = r.Registry.UpdateSyncResult(
			app.ID,
			"error",
			now,
			app.LastSyncedCommit,
			app.LastSyncedCommitMessage,
			output,
			err.Error(),
		)
		return err
	}

	now := time.Now()
	if err := r.Registry.UpdateSyncResult(
		app.ID,
		"synced",
		now,
		app.LastSeenCommit,
		app.LastSeenCommitMessage,
		output,
		"",
	); err != nil && r.Logger != nil {
		r.Logger.Warn("Failed to update app status", "app_id", app.ID, "error", err)
	}
	return nil
}

func truncateOutput(value string) string {
	const maxLen = 2000
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= maxLen {
		return trimmed
	}
	return trimmed[:maxLen] + "...(truncated)"
}
