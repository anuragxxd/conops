package controller

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/conops/conops/internal/repoauth"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

// GitWatcher monitors registered apps for changes.
type GitWatcher struct {
	Registry *Registry
	Logger   *slog.Logger
	CacheDir string
}

// NewGitWatcher creates a new Git watcher.
func NewGitWatcher(registry *Registry, logger *slog.Logger) *GitWatcher {
	return &GitWatcher{
		Registry: registry,
		Logger:   logger,
		CacheDir: "./.conops-cache",
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

	auth, err := w.authForApp(app)
	if err != nil {
		return err
	}

	var repo *git.Repository

	// Clone if not exists, otherwise open
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		w.Logger.Info("Cloning repo", "id", app.ID, "repo", app.RepoURL)
		repo, err = git.PlainClone(repoPath, false, &git.CloneOptions{
			URL:      app.RepoURL,
			Progress: nil,
			Auth:     auth,
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
		Auth:       auth,
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
	commitMessage := ""
	if commitObj, commitErr := repo.CommitObject(ref.Hash()); commitErr == nil {
		commitMessage = commitSubject(commitObj.Message)
	}
	w.Logger.Debug("HEAD resolved", "id", app.ID, "commit", commitHash)

	// Check if changed
	if commitHash == app.LastSeenCommit {
		w.Logger.Debug("No new commit detected", "id", app.ID, "commit", commitHash)
		return nil
	}

	w.Logger.Info("New commit detected", "id", app.ID, "commit", commitHash)

	// Update registry
	if err := w.Registry.UpdateCommitWithMessage(app.ID, commitHash, commitMessage); err != nil {
		return err
	}
	app.LastSeenCommit = commitHash
	app.LastSeenCommitMessage = commitMessage
	w.Logger.Debug("Registry updated", "id", app.ID, "commit", commitHash)

	return nil
}

func commitSubject(message string) string {
	for _, line := range strings.Split(strings.ReplaceAll(message, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (w *GitWatcher) authForApp(app *App) (transport.AuthMethod, error) {
	if app.RepoAuthMethod != repoauth.MethodDeployKey {
		return nil, nil
	}

	deployKey, err := w.Registry.GetDeployKey(app.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load deploy key: %w", err)
	}
	if len(deployKey) == 0 {
		return nil, fmt.Errorf("missing deploy key for app")
	}
	defer zeroBytes(deployKey)

	knownHostsPath, err := repoauth.ResolveKnownHostsPath()
	if err != nil {
		return nil, err
	}
	callback, err := repoauth.NewHostKeyCallback(knownHostsPath)
	if err != nil {
		return nil, err
	}

	auth, err := gitssh.NewPublicKeys("git", deployKey, "")
	if err != nil {
		return nil, fmt.Errorf("invalid deploy key: %w", err)
	}
	auth.HostKeyCallbackHelper = gitssh.HostKeyCallbackHelper{
		HostKeyCallback: callback,
	}
	return auth, nil
}
