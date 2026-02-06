package compose

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
	"unicode"
)

// ComposeExecutor handles Docker Compose operations.
type ComposeExecutor struct {
	WorkDir string
	Logger  *slog.Logger
}

// NewComposeExecutor creates a new executor.
func NewComposeExecutor(logger *slog.Logger) *ComposeExecutor {
	return &ComposeExecutor{
		WorkDir: "./.conops-runtime",
		Logger:  logger,
	}
}

// Apply executes the compose file.
func (e *ComposeExecutor) Apply(ctx context.Context, appID, content string, envVars map[string]string, repoURL, branch, composePath, commitHash string) (string, error) {
	appDir := filepath.Join(e.WorkDir, appID)
	appDirAbs, err := filepath.Abs(appDir)
	if err != nil {
		return "", fmt.Errorf("resolve app dir failed: %w", err)
	}
	if err := os.MkdirAll(appDirAbs, 0755); err != nil {
		return "", fmt.Errorf("failed to create app dir: %w", err)
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
	e.Logger.Info("Preparing repo", "app_id", appID, "repo", repoURL, "branch", branch, "commit", commitHash, "dir", repoDir)
	if err := e.prepareRepo(ctx, appDirAbs, repoDir, repoURL, branch, commitHash); err != nil {
		return "", fmt.Errorf("prepare repo failed: %w", err)
	}

	composeFullPath := filepath.Join(repoDir, composePath)
	composeDir := filepath.Dir(composeFullPath)
	if _, err := os.Stat(composeDir); err != nil {
		return "", fmt.Errorf("compose dir not found: %w", err)
	}

	wroteCompose := false
	if strings.TrimSpace(content) != "" {
		if err := os.WriteFile(composeFullPath, []byte(content), 0644); err != nil {
			return "", fmt.Errorf("failed to write compose file: %w", err)
		}
		wroteCompose = true
	} else {
		if _, err := os.Stat(composeFullPath); err != nil {
			return "", fmt.Errorf("compose file not found: %w", err)
		}
	}
	composeFileName := filepath.Base(composeFullPath)
	projectName := composeProjectName(appID)

	e.Logger.Info(
		"Compose file ready",
		"app_id", appID,
		"path", composeFullPath,
		"bytes", len(content),
		"written", wroteCompose,
	)

	// Pull images
	e.Logger.Info("Pulling images", "app_id", appID)
	pullOut, err := e.runCommand(ctx, "docker", []string{"compose", "-p", projectName, "-f", composeFileName, "pull"}, composeDir, envVars)
	if err != nil {
		return string(pullOut), fmt.Errorf("pull failed: %w", err)
	}

	// Up detached
	e.Logger.Info("Applying configuration", "app_id", appID)
	upOut, err := e.runCommand(ctx, "docker", []string{"compose", "-p", projectName, "-f", composeFileName, "up", "-d", "--remove-orphans"}, composeDir, envVars)
	if err != nil {
		return string(pullOut) + "\n" + string(upOut), fmt.Errorf("up failed: %w", err)
	}

	return string(pullOut) + "\n" + string(upOut), nil
}

// Destroy tears down app containers and networks without removing volumes.
func (e *ComposeExecutor) Destroy(ctx context.Context, appID, composePath string, envVars map[string]string) (string, error) {
	projectName := composeProjectName(appID)
	appDirAbs, err := filepath.Abs(filepath.Join(e.WorkDir, appID))
	if err != nil {
		return "", fmt.Errorf("resolve app dir failed: %w", err)
	}

	repoDir := filepath.Join(appDirAbs, "repo")
	if strings.TrimSpace(composePath) == "" {
		composePath = "compose.yaml"
	}
	composeFullPath := filepath.Join(repoDir, composePath)
	composeDir := filepath.Dir(composeFullPath)
	composeFileName := filepath.Base(composeFullPath)

	var outputs []string
	downAttempted := false

	if fileInfo, statErr := os.Stat(composeFullPath); statErr == nil && !fileInfo.IsDir() {
		downAttempted = true
		e.Logger.Info("Stopping app stack", "app_id", appID, "project", projectName, "compose_file", composeFullPath)
		downOut, downErr := e.runCommand(
			ctx,
			"docker",
			[]string{"compose", "-p", projectName, "-f", composeFileName, "down", "--remove-orphans"},
			composeDir,
			envVars,
		)
		if strings.TrimSpace(downOut) != "" {
			outputs = append(outputs, downOut)
		}
		if downErr != nil {
			return strings.Join(outputs, "\n"), fmt.Errorf("compose down failed: %w", downErr)
		}
	}

	// Fallback cleanup for any lingering containers (including legacy runs before project naming).
	legacyWorkingDir := composeDir
	containerIDs, listErr := e.listContainerIDsForCleanup(ctx, projectName, legacyWorkingDir)
	if listErr != nil {
		return strings.Join(outputs, "\n"), listErr
	}
	if len(containerIDs) > 0 {
		e.Logger.Info("Removing lingering containers", "app_id", appID, "containers", len(containerIDs))
		args := append([]string{"rm", "-f"}, containerIDs...)
		rmOut, rmErr := e.runCommand(ctx, "docker", args, appDirAbs, nil)
		if strings.TrimSpace(rmOut) != "" {
			outputs = append(outputs, rmOut)
		}
		if rmErr != nil {
			return strings.Join(outputs, "\n"), fmt.Errorf("docker rm failed: %w", rmErr)
		}
	}

	if !downAttempted && len(containerIDs) == 0 && e.Logger != nil {
		e.Logger.Info("No running resources found for app", "app_id", appID, "project", projectName)
	}

	if removeErr := os.RemoveAll(appDirAbs); removeErr != nil {
		e.Logger.Warn("Failed to remove app runtime directory", "app_id", appID, "dir", appDirAbs, "error", removeErr)
	}

	return strings.Join(outputs, "\n"), nil
}

func (e *ComposeExecutor) prepareRepo(ctx context.Context, appDir, repoDir, repoURL, branch, commitHash string) error {
	gitDir := filepath.Join(repoDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		cloneArgs := []string{"clone", "--branch", branch}
		if commitHash == "" {
			cloneArgs = append(cloneArgs, "--depth", "1")
		}
		cloneArgs = append(cloneArgs, repoURL, repoDir)
		_, err := e.runCommand(ctx, "git", cloneArgs, appDir, nil)
		if err != nil {
			return err
		}
		if commitHash != "" {
			if _, err := e.runCommand(ctx, "git", []string{"checkout", commitHash}, repoDir, nil); err != nil {
				return err
			}
		}
		return nil
	}

	if _, err := e.runCommand(ctx, "git", []string{"fetch", "origin"}, repoDir, nil); err != nil {
		return err
	}

	if commitHash != "" {
		if _, err := e.runCommand(ctx, "git", []string{"fetch", "origin", commitHash}, repoDir, nil); err != nil {
			return err
		}
		if _, err := e.runCommand(ctx, "git", []string{"checkout", commitHash}, repoDir, nil); err != nil {
			return err
		}
		if _, err := e.runCommand(ctx, "git", []string{"reset", "--hard", commitHash}, repoDir, nil); err != nil {
			return err
		}
	} else {
		if _, err := e.runCommand(ctx, "git", []string{"checkout", branch}, repoDir, nil); err != nil {
			return err
		}
		if _, err := e.runCommand(ctx, "git", []string{"reset", "--hard", "origin/" + branch}, repoDir, nil); err != nil {
			return err
		}
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

func (e *ComposeExecutor) listContainerIDsForCleanup(ctx context.Context, projectName, workingDir string) ([]string, error) {
	idSet := make(map[string]struct{})
	workDir := e.WorkDir
	if workDir == "" {
		workDir = "."
	}
	if _, err := os.Stat(workDir); err != nil {
		workDir = "."
	}

	byProject, err := e.listContainerIDsByFilter(ctx, []string{fmt.Sprintf("label=com.docker.compose.project=%s", projectName)}, workDir)
	if err != nil {
		return nil, err
	}
	for _, id := range byProject {
		idSet[id] = struct{}{}
	}

	if strings.TrimSpace(workingDir) != "" {
		byWorkingDir, err := e.listContainerIDsByFilter(ctx, []string{fmt.Sprintf("label=com.docker.compose.project.working_dir=%s", workingDir)}, workDir)
		if err != nil {
			return nil, err
		}
		for _, id := range byWorkingDir {
			idSet[id] = struct{}{}
		}
	}

	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}

func (e *ComposeExecutor) listContainerIDsByFilter(ctx context.Context, filters []string, workDir string) ([]string, error) {
	args := []string{"ps", "-aq"}
	for _, filter := range filters {
		args = append(args, "--filter", filter)
	}

	output, err := e.runCommand(ctx, "docker", args, workDir, nil)
	if err != nil {
		return nil, fmt.Errorf("docker ps failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	ids := make([]string, 0, len(lines))
	for _, line := range lines {
		id := strings.TrimSpace(line)
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func composeProjectName(appID string) string {
	const fallback = "conops-app"
	raw := strings.ToLower(strings.TrimSpace(appID))
	if raw == "" {
		return fallback
	}

	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}

	project := strings.Trim(b.String(), "-_")
	if project == "" {
		project = fallback
	}
	if !startsWithAlphaNum(project) {
		project = "app-" + project
	}
	if len(project) > 63 {
		project = project[:63]
		project = strings.Trim(project, "-_")
	}
	if project == "" {
		return fallback
	}
	return project
}

func startsWithAlphaNum(value string) bool {
	if value == "" {
		return false
	}
	for _, first := range value {
		return unicode.IsLetter(first) || unicode.IsDigit(first)
	}
	return false
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
