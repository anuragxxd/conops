package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// ComposeExecutor handles Docker Compose operations.
type ComposeExecutor struct {
	WorkDir string
	Logger  *slog.Logger
}

// NewComposeExecutor creates a new executor.
func NewComposeExecutor(logger *slog.Logger) *ComposeExecutor {
	return &ComposeExecutor{
		WorkDir: "./.conops-agent",
		Logger:  logger,
	}
}

// Apply executes the compose file.
func (e *ComposeExecutor) Apply(ctx context.Context, appID, content string, envVars map[string]string, repoURL, branch, composePath string) (string, error) {
	appDir := filepath.Join(e.WorkDir, appID)
	appDirAbs, err := filepath.Abs(appDir)
	if err != nil {
		return "", fmt.Errorf("resolve app dir failed: %w", err)
	}
	if err := os.MkdirAll(appDirAbs, 0755); err != nil {
		return "", fmt.Errorf("failed to create app dir: %w", err)
	}

	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("compose content is empty")
	}

	if strings.TrimSpace(repoURL) == "" {
		return "", fmt.Errorf("repo url is empty")
	}
	if strings.TrimSpace(composePath) == "" {
		return "", fmt.Errorf("compose path is empty")
	}
	if strings.TrimSpace(branch) == "" {
		branch = "main"
	}

	repoDir := filepath.Join(appDirAbs, "repo")
	e.Logger.Info("Preparing repo", "app_id", appID, "repo", repoURL, "branch", branch, "dir", repoDir)
	if err := e.prepareRepo(ctx, appDirAbs, repoDir, repoURL, branch); err != nil {
		return "", fmt.Errorf("prepare repo failed: %w", err)
	}

	composeFullPath := filepath.Join(repoDir, composePath)
	composeDir := filepath.Dir(composeFullPath)
	if _, err := os.Stat(composeDir); err != nil {
		return "", fmt.Errorf("compose dir not found: %w", err)
	}

	if err := os.WriteFile(composeFullPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to write compose file: %w", err)
	}
	composeFileName := filepath.Base(composeFullPath)

	e.Logger.Info(
		"Compose file written",
		"app_id", appID,
		"path", composeFullPath,
		"bytes", len(content),
	)

	// Pull images
	e.Logger.Info("Pulling images", "app_id", appID)
	pullOut, err := e.runCommand(ctx, "docker", []string{"compose", "-f", composeFileName, "pull"}, composeDir, envVars)
	if err != nil {
		return string(pullOut), fmt.Errorf("pull failed: %w", err)
	}

	// Up detached
	e.Logger.Info("Applying configuration", "app_id", appID)
	upOut, err := e.runCommand(ctx, "docker", []string{"compose", "-f", composeFileName, "up", "-d", "--remove-orphans"}, composeDir, envVars)
	if err != nil {
		return string(pullOut) + "\n" + string(upOut), fmt.Errorf("up failed: %w", err)
	}

	return string(pullOut) + "\n" + string(upOut), nil
}

func (e *ComposeExecutor) prepareRepo(ctx context.Context, appDir, repoDir, repoURL, branch string) error {
	gitDir := filepath.Join(repoDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		_, err := e.runCommand(ctx, "git", []string{"clone", "--branch", branch, "--depth", "1", repoURL, repoDir}, appDir, nil)
		if err != nil {
			return err
		}
		return nil
	}

	if _, err := e.runCommand(ctx, "git", []string{"fetch", "origin"}, repoDir, nil); err != nil {
		return err
	}
	if _, err := e.runCommand(ctx, "git", []string{"checkout", branch}, repoDir, nil); err != nil {
		return err
	}
	if _, err := e.runCommand(ctx, "git", []string{"reset", "--hard", "origin/" + branch}, repoDir, nil); err != nil {
		return err
	}
	if _, err := e.runCommand(ctx, "git", []string{"clean", "-fd"}, repoDir, nil); err != nil {
		return err
	}
	return nil
}

func (e *ComposeExecutor) runCommand(ctx context.Context, cmd string, args []string, workDir string, env map[string]string) (string, error) {
	start := time.Now()
	command := exec.CommandContext(ctx, cmd, args...)
	command.Dir = workDir

	if len(env) > 0 {
		mergedEnv := append([]string{}, os.Environ()...)
		for key, value := range env {
			mergedEnv = append(mergedEnv, fmt.Sprintf("%s=%s", key, value))
		}
		command.Env = mergedEnv
	}

	e.Logger.Info(
		"Executing command",
		"cmd", command.String(),
		"dir", workDir,
		"env_keys", sortedKeys(env),
	)

	output, err := command.CombinedOutput()
	trimmed := strings.TrimSpace(string(output))
	if trimmed != "" {
		e.Logger.Info("Command output", "cmd", command.String(), "output", truncateOutput(trimmed))
	}

	if err != nil {
		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) {
			e.Logger.Error("Command context ended", "cmd", command.String(), "context_err", ctx.Err())
		}
		e.Logger.Error(
			"Command failed",
			"cmd", command.String(),
			"dir", workDir,
			"exit_code", exitCode,
			"elapsed_ms", time.Since(start).Milliseconds(),
		)
		return string(output), fmt.Errorf("command failed: %w", err)
	}

	e.Logger.Info(
		"Command succeeded",
		"cmd", command.String(),
		"elapsed_ms", time.Since(start).Milliseconds(),
	)

	return string(output), nil
}

func sortedKeys(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func truncateOutput(value string) string {
	const maxLen = 2000
	if len(value) <= maxLen {
		return value
	}
	return value[:maxLen] + "...(truncated)"
}
