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

	"github.com/conops/conops/internal/repoauth"
)

// ComposeExecutor handles Docker Compose operations.
type ComposeExecutor struct {
	WorkDir string
	Logger  *slog.Logger
}

// ProjectRuntimeState summarizes runtime state for one compose project.
type ProjectRuntimeState struct {
	ContainerCount int
	RunningCount   int
	ExitedCount    int
	UnhealthyCount int
}

// IsHealthy reports whether all tracked service containers are running and healthy.
func (s ProjectRuntimeState) IsHealthy() bool {
	if s.ContainerCount == 0 {
		return false
	}
	return s.RunningCount == s.ContainerCount && s.ExitedCount == 0 && s.UnhealthyCount == 0
}

// NewComposeExecutor creates a new executor.
func NewComposeExecutor(logger *slog.Logger) *ComposeExecutor {
	return &ComposeExecutor{
		WorkDir: "./.conops-runtime",
		Logger:  logger,
	}
}

// Apply executes the compose file.
func (e *ComposeExecutor) Apply(ctx context.Context, appID, content string, envVars map[string]string, repoURL, branch, composePath, commitHash string, deployKey []byte) (string, error) {
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
	if err := e.prepareRepo(ctx, appDirAbs, repoDir, repoURL, branch, commitHash, deployKey); err != nil {
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

// SnapshotProjects captures compose runtime status from Docker for all projects.
func (e *ComposeExecutor) SnapshotProjects(ctx context.Context) (map[string]ProjectRuntimeState, error) {
	output, err := e.runCommand(
		ctx,
		"docker",
		[]string{
			"ps",
			"-a",
			"--format",
			`{{.Label "com.docker.compose.project"}}|{{.Label "com.docker.compose.oneoff"}}|{{.Status}}`,
		},
		e.runtimeWorkDir(),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("docker ps failed: %w", err)
	}

	snapshot := make(map[string]ProjectRuntimeState)
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}

		projectName := strings.TrimSpace(parts[0])
		oneOff := strings.TrimSpace(parts[1])
		status := strings.TrimSpace(parts[2])
		if projectName == "" || strings.EqualFold(oneOff, "true") || oneOff == "1" {
			continue
		}

		state := snapshot[projectName]
		state.ContainerCount++
		if dockerStatusIsRunning(status) {
			state.RunningCount++
		} else {
			state.ExitedCount++
		}
		if strings.Contains(strings.ToLower(status), "(unhealthy)") {
			state.UnhealthyCount++
		}
		snapshot[projectName] = state
	}

	return snapshot, nil
}

func (e *ComposeExecutor) prepareRepo(ctx context.Context, appDir, repoDir, repoURL, branch, commitHash string, deployKey []byte) error {
	gitEnv, cleanup, err := e.buildGitEnv(appDir, deployKey)
	if err != nil {
		return err
	}
	defer cleanup()

	gitDir := filepath.Join(repoDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		cloneArgs := []string{"clone", "--branch", branch}
		if commitHash == "" {
			cloneArgs = append(cloneArgs, "--depth", "1")
		}
		cloneArgs = append(cloneArgs, repoURL, repoDir)
		_, err := e.runCommand(ctx, "git", cloneArgs, appDir, gitEnv)
		if err != nil {
			return err
		}
		if commitHash != "" {
			if _, err := e.runCommand(ctx, "git", []string{"checkout", commitHash}, repoDir, gitEnv); err != nil {
				return err
			}
		}
		return nil
	}

	if _, err := e.runCommand(ctx, "git", []string{"fetch", "origin"}, repoDir, gitEnv); err != nil {
		return err
	}

	if commitHash != "" {
		if _, err := e.runCommand(ctx, "git", []string{"fetch", "origin", commitHash}, repoDir, gitEnv); err != nil {
			return err
		}
		if _, err := e.runCommand(ctx, "git", []string{"checkout", commitHash}, repoDir, gitEnv); err != nil {
			return err
		}
		if _, err := e.runCommand(ctx, "git", []string{"reset", "--hard", commitHash}, repoDir, gitEnv); err != nil {
			return err
		}
	} else {
		if _, err := e.runCommand(ctx, "git", []string{"checkout", branch}, repoDir, gitEnv); err != nil {
			return err
		}
		if _, err := e.runCommand(ctx, "git", []string{"reset", "--hard", "origin/" + branch}, repoDir, gitEnv); err != nil {
			return err
		}
	}
	if _, err := e.runCommand(ctx, "git", []string{"clean", "-fd"}, repoDir, gitEnv); err != nil {
		return err
	}
	return nil
}

func (e *ComposeExecutor) buildGitEnv(appDir string, deployKey []byte) (map[string]string, func(), error) {
	if len(deployKey) == 0 {
		return nil, func() {}, nil
	}

	knownHostsPath, err := repoauth.ResolveKnownHostsPath()
	if err != nil {
		return nil, nil, err
	}

	sshDir := filepath.Join(appDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return nil, nil, fmt.Errorf("failed to create ssh dir: %w", err)
	}

	keyPath := filepath.Join(sshDir, "deploy_key")
	if err := os.WriteFile(keyPath, deployKey, 0600); err != nil {
		return nil, nil, fmt.Errorf("failed to write deploy key file: %w", err)
	}

	cleanup := func() {
		_ = os.Remove(keyPath)
	}

	env := map[string]string{
		"GIT_TERMINAL_PROMPT": "0",
		"GIT_SSH_COMMAND":     repoauth.BuildSSHCommand(keyPath, knownHostsPath),
	}
	return env, cleanup, nil
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
	workDir := e.runtimeWorkDir()

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

func (e *ComposeExecutor) runtimeWorkDir() string {
	workDir := strings.TrimSpace(e.WorkDir)
	if workDir == "" {
		return "."
	}
	if _, err := os.Stat(workDir); err != nil {
		return "."
	}
	return workDir
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

// ServiceContainer represents runtime details for one container in a compose project.
type ServiceContainer struct {
	Service string
	Name    string
	Image   string
	Status  string // "running" or "exited"
	Health  string // "healthy", "unhealthy", "starting", or "" (no healthcheck)
	Ports   string
}

// InspectProjectContainers returns detailed container information for a compose project.
func (e *ComposeExecutor) InspectProjectContainers(ctx context.Context, projectName string) ([]ServiceContainer, error) {
	if strings.TrimSpace(projectName) == "" {
		return nil, nil
	}

	output, err := e.runCommand(
		ctx,
		"docker",
		[]string{
			"ps", "-a",
			"--filter", fmt.Sprintf("label=com.docker.compose.project=%s", projectName),
			"--filter", "label=com.docker.compose.oneoff=False",
			"--format", `{{.Label "com.docker.compose.service"}}|{{.Image}}|{{.Status}}|{{.Ports}}|{{.Names}}`,
		},
		e.runtimeWorkDir(),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("docker ps failed: %w", err)
	}

	var containers []ServiceContainer
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 5)
		if len(parts) != 5 {
			continue
		}

		rawStatus := strings.TrimSpace(parts[2])
		health := ""
		lowerStatus := strings.ToLower(rawStatus)
		if strings.Contains(lowerStatus, "(healthy)") {
			health = "healthy"
		} else if strings.Contains(lowerStatus, "(unhealthy)") {
			health = "unhealthy"
		} else if strings.Contains(lowerStatus, "(health: starting)") {
			health = "starting"
		}

		state := "exited"
		if dockerStatusIsRunning(rawStatus) {
			state = "running"
		}

		containers = append(containers, ServiceContainer{
			Service: strings.TrimSpace(parts[0]),
			Image:   strings.TrimSpace(parts[1]),
			Status:  state,
			Health:  health,
			Ports:   strings.TrimSpace(parts[3]),
			Name:    strings.TrimSpace(parts[4]),
		})
	}

	slices.SortFunc(containers, func(a, b ServiceContainer) int {
		return strings.Compare(a.Service, b.Service)
	})

	return containers, nil
}

// ProjectNameForApp returns the deterministic compose project name for an app ID.
func ProjectNameForApp(appID string) string {
	return composeProjectName(appID)
}

func dockerStatusIsRunning(status string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(status)), "up ")
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
