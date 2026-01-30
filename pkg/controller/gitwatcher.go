package controller

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
)

// PendingTask represents a deployment task generated from a git commit.
type PendingTask struct {
	AppID          string
	CommitHash     string
	ComposeContent string
	CreatedAt      time.Time
	RepoURL        string
	Branch         string
	ComposePath    string
}

// GitWatcher monitors registered apps for changes.
type GitWatcher struct {
	Registry  *Registry
	Logger    *slog.Logger
	TaskQueue *TaskQueue
	CacheDir  string
}

// NewGitWatcher creates a new Git watcher.
func NewGitWatcher(registry *Registry, taskQueue *TaskQueue, logger *slog.Logger) *GitWatcher {
	return &GitWatcher{
		Registry:  registry,
		TaskQueue: taskQueue,
		Logger:    logger,
		CacheDir:  "./.conops-cache",
	}
}

// Start begins the polling loop.
func (w *GitWatcher) Start(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second) // Check registry for new apps every 10s
	defer ticker.Stop()

	// Track running pollers to avoid duplicates
	pollers := make(map[string]context.CancelFunc)

	for {
		select {
		case <-ctx.Done():
			w.Logger.Info("Git watcher stopped")
			return
		case <-ticker.C:
			apps := w.Registry.List()
			w.Logger.Debug("Registry poll tick", "app_count", len(apps))
			activeIDs := make(map[string]bool)

			for _, app := range apps {
				activeIDs[app.ID] = true
				if _, running := pollers[app.ID]; !running {
					// Start a new poller for this app
					pollCtx, cancel := context.WithCancel(ctx)
					pollers[app.ID] = cancel
					w.Logger.Info("Starting app poller", "id", app.ID, "interval", app.PollInterval)
					go w.pollApp(pollCtx, app)
				}
			}

			// Cleanup stopped apps
			for id, cancel := range pollers {
				if !activeIDs[id] {
					cancel()
					delete(pollers, id)
					w.Logger.Info("Stopped app poller", "id", id)
				}
			}
		}
	}
}

func (w *GitWatcher) pollApp(ctx context.Context, app *App) {
	interval, err := time.ParseDuration(app.PollInterval)
	if err != nil {
		w.Logger.Warn("Invalid poll interval, using default", "id", app.ID, "interval", app.PollInterval)
		interval = 30 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	w.Logger.Info("Started polling app", "id", app.ID, "repo", app.RepoURL)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.Logger.Debug("Polling repo", "id", app.ID, "repo", app.RepoURL, "branch", app.Branch)
			if err := w.checkRepo(app); err != nil {
				w.Logger.Error("Failed to check repo", "id", app.ID, "error", err)
			}
		}
	}
}

func (w *GitWatcher) checkRepo(app *App) error {
	repoPath := filepath.Join(w.CacheDir, app.ID)
	w.Logger.Debug("Checking repo state", "id", app.ID, "path", repoPath)

	var repo *git.Repository
	var err error

	// Clone if not exists, otherwise open
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		w.Logger.Info("Cloning repo", "id", app.ID, "repo", app.RepoURL)
		repo, err = git.PlainClone(repoPath, false, &git.CloneOptions{
			URL:      app.RepoURL,
			Progress: nil,
		})
	} else {
		w.Logger.Debug("Opening repo", "id", app.ID, "path", repoPath)
		repo, err = git.PlainOpen(repoPath)
	}

	if err != nil {
		return fmt.Errorf("git error: %w", err)
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("worktree error: %w", err)
	}

	// Pull latest changes
	w.Logger.Debug("Fetching latest", "id", app.ID, "remote", "origin")
	
	// First fetch all changes
	err = repo.Fetch(&git.FetchOptions{
		RemoteName: "origin",
		Progress:   nil,
		RefSpecs:   []config.RefSpec{"+refs/heads/*:refs/remotes/origin/*"},
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("fetch error: %w", err)
	}
	if err == git.NoErrAlreadyUpToDate {
		w.Logger.Debug("Fetch up to date", "id", app.ID)
	}

	// Resolve remote branch and checkout its latest commit
	remoteRefName := plumbing.NewRemoteReferenceName("origin", app.Branch)
	remoteRef, refErr := repo.Reference(remoteRefName, true)
	if refErr != nil {
		return fmt.Errorf("remote branch not found: %w", refErr)
	}
	w.Logger.Debug("Checking out remote commit", "id", app.ID, "branch", app.Branch, "remote_hash", remoteRef.Hash().String())
	err = worktree.Checkout(&git.CheckoutOptions{
		Hash:  remoteRef.Hash(),
		Force: true,
	})
	if err != nil {
		return fmt.Errorf("checkout error: %w", err)
	}

	// Get HEAD commit
	ref, err := repo.Head()
	if err != nil {
		return fmt.Errorf("head error: %w", err)
	}
	commitHash := ref.Hash().String()
	w.Logger.Debug("HEAD resolved", "id", app.ID, "commit", commitHash)

	// Check if changed
	if commitHash == app.LastSeenCommit {
		w.Logger.Debug("No new commit detected", "id", app.ID, "commit", commitHash)
		return nil
	}

	w.Logger.Info("New commit detected", "id", app.ID, "commit", commitHash)

	// Read compose file
	composePath := filepath.Join(repoPath, app.ComposePath)
	w.Logger.Debug("Reading compose file", "id", app.ID, "path", composePath)
	composeFile, err := os.ReadFile(composePath)
	if err != nil {
		return fmt.Errorf("failed to read compose file: %w", err)
	}
	w.Logger.Info("Compose file read", "id", app.ID, "path", composePath, "bytes", len(composeFile))

	// Update registry
	if err := w.Registry.UpdateCommit(app.ID, commitHash); err != nil {
		return err
	}
	w.Logger.Debug("Registry updated", "id", app.ID, "commit", commitHash)

	// Generate task
	task := PendingTask{
		AppID:          app.ID,
		CommitHash:     commitHash,
		ComposeContent: string(composeFile),
		CreatedAt:      time.Now(),
		RepoURL:        app.RepoURL,
		Branch:         app.Branch,
		ComposePath:    app.ComposePath,
	}

	w.TaskQueue.Enqueue(task)
	w.Logger.Info("Task generated and queued", "id", app.ID, "compose_bytes", len(composeFile))

	return nil
}
