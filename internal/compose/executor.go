package compose

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/conops/conops/internal/repoauth"
)

// ComposeExecutor handles Docker Compose operations.
type ComposeExecutor struct {
	WorkDir  string
	ToolsDir string
	Logger   *slog.Logger

	toolchainMu      sync.Mutex
	dockerResolution dockerCommandResolution
	resolutionAt     time.Time
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
		WorkDir:  "./.conops-runtime",
		ToolsDir: "./.conops-tools",
		Logger:   logger,
	}
}

// Apply executes the compose file.
func (e *ComposeExecutor) Apply(
	ctx context.Context,
	appID, content string,
	envVars map[string]string,
	repoURL, branch, composePath, commitHash string,
	deployKey []byte,
	onProgress func(string),
) (string, error) {
	var syncLog strings.Builder
	emitProgress := func() {
		if onProgress != nil {
			onProgress(strings.TrimSpace(syncLog.String()))
		}
	}

	appDir := filepath.Join(e.WorkDir, appID)
	appDirAbs, err := filepath.Abs(appDir)
	if err != nil {
		appendLogSection(&syncLog, "Sync setup")
		appendLogLine(&syncLog, "failed to resolve runtime directory")
		appendLogLine(&syncLog, err.Error())
		emitProgress()
		return strings.TrimSpace(syncLog.String()), fmt.Errorf("resolve app dir failed: %w", err)
	}
	if err := os.MkdirAll(appDirAbs, 0755); err != nil {
		appendLogSection(&syncLog, "Sync setup")
		appendLogLine(&syncLog, "failed to create runtime directory")
		appendLogLine(&syncLog, err.Error())
		emitProgress()
		return strings.TrimSpace(syncLog.String()), fmt.Errorf("failed to create app dir: %w", err)
	}

	if strings.TrimSpace(repoURL) == "" {
		appendLogSection(&syncLog, "Validation")
		appendLogLine(&syncLog, "repo url is empty")
		emitProgress()
		return strings.TrimSpace(syncLog.String()), fmt.Errorf("repo url is empty")
	}
	if strings.TrimSpace(composePath) == "" {
		appendLogSection(&syncLog, "Validation")
		appendLogLine(&syncLog, "compose path is empty")
		emitProgress()
		return strings.TrimSpace(syncLog.String()), fmt.Errorf("compose path is empty")
	}
	if strings.TrimSpace(branch) == "" {
		branch = "main"
	}

	appendLogSection(&syncLog, "Sync started")
	appendLogLine(&syncLog, fmt.Sprintf("app_id: %s", appID))
	appendLogLine(&syncLog, fmt.Sprintf("repository: %s", repoURL))
	appendLogLine(&syncLog, fmt.Sprintf("branch: %s", branch))
	if strings.TrimSpace(commitHash) != "" {
		appendLogLine(&syncLog, fmt.Sprintf("target_commit: %s", commitHash))
	} else {
		appendLogLine(&syncLog, "target_commit: latest on branch")
	}
	emitProgress()

	appendLogSection(&syncLog, "Docker preflight")
	preflight, err := e.ensureDockerPreflight(ctx)
	if err != nil {
		appendLogLine(&syncLog, "failed")
		appendLogLine(&syncLog, err.Error())
		emitProgress()
		return strings.TrimSpace(syncLog.String()), fmt.Errorf("docker preflight failed: %w", err)
	}
	for _, line := range preflight.LogLines() {
		appendLogLine(&syncLog, line)
	}
	emitProgress()

	repoDir := filepath.Join(appDirAbs, "repo")
	e.Logger.Info("Preparing repo", "app_id", appID, "repo", repoURL, "branch", branch, "commit", commitHash, "dir", repoDir)
	repoLog, err := e.prepareRepo(ctx, appDirAbs, repoDir, repoURL, branch, commitHash, deployKey)
	appendLogBlock(&syncLog, repoLog)
	emitProgress()
	if err != nil {
		return strings.TrimSpace(syncLog.String()), fmt.Errorf("prepare repo failed: %w", err)
	}

	composeFullPath := filepath.Join(repoDir, composePath)
	composeDir := filepath.Dir(composeFullPath)
	if _, err := os.Stat(composeDir); err != nil {
		appendLogSection(&syncLog, "Compose file")
		appendLogLine(&syncLog, fmt.Sprintf("compose directory does not exist: %s", composeDir))
		appendLogLine(&syncLog, err.Error())
		emitProgress()
		return strings.TrimSpace(syncLog.String()), fmt.Errorf("compose dir not found: %w", err)
	}

	wroteCompose := false
	if strings.TrimSpace(content) != "" {
		if err := os.WriteFile(composeFullPath, []byte(content), 0644); err != nil {
			appendLogSection(&syncLog, "Compose file")
			appendLogLine(&syncLog, fmt.Sprintf("failed to write compose file: %s", composeFullPath))
			appendLogLine(&syncLog, err.Error())
			emitProgress()
			return strings.TrimSpace(syncLog.String()), fmt.Errorf("failed to write compose file: %w", err)
		}
		wroteCompose = true
	} else {
		if _, err := os.Stat(composeFullPath); err != nil {
			appendLogSection(&syncLog, "Compose file")
			appendLogLine(&syncLog, fmt.Sprintf("compose file not found: %s", composeFullPath))
			appendLogLine(&syncLog, err.Error())
			emitProgress()
			return strings.TrimSpace(syncLog.String()), fmt.Errorf("compose file not found: %w", err)
		}
	}
	composeFileName := filepath.Base(composeFullPath)
	projectName := composeProjectName(appID)

	appendLogSection(&syncLog, "Compose file")
	appendLogLine(&syncLog, fmt.Sprintf("path: %s", composeFullPath))
	appendLogLine(&syncLog, fmt.Sprintf("written_from_request: %t", wroteCompose))
	emitProgress()

	e.Logger.Info(
		"Compose file ready",
		"app_id", appID,
		"path", composeFullPath,
		"bytes", len(content),
		"written", wroteCompose,
	)

	// Pull images
	appendLogSection(&syncLog, "Docker image pull")
	e.Logger.Info("Pulling images", "app_id", appID)
	_, err = e.runCommandWithTranscript(
		ctx,
		&syncLog,
		"docker",
		[]string{"compose", "-p", projectName, "-f", composeFileName, "pull"},
		composeDir,
		envVars,
		onProgress,
	)
	if err != nil {
		return strings.TrimSpace(syncLog.String()), fmt.Errorf("pull failed: %w", err)
	}

	// Up detached
	appendLogSection(&syncLog, "Compose apply")
	appendLogLine(&syncLog, "build output appears below when services require a build")
	e.Logger.Info("Applying configuration", "app_id", appID)
	_, err = e.runCommandWithTranscript(
		ctx,
		&syncLog,
		"docker",
		[]string{"compose", "-p", projectName, "-f", composeFileName, "up", "-d", "--remove-orphans"},
		composeDir,
		envVars,
		onProgress,
	)
	if err != nil {
		return strings.TrimSpace(syncLog.String()), fmt.Errorf("up failed: %w", err)
	}

	appendLogSection(&syncLog, "Sync completed")
	appendLogLine(&syncLog, "application reconciled successfully")
	emitProgress()
	return strings.TrimSpace(syncLog.String()), nil
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
			nil,
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
		rmOut, rmErr := e.runCommand(ctx, "docker", args, appDirAbs, nil, nil)
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

func (e *ComposeExecutor) prepareRepo(ctx context.Context, appDir, repoDir, repoURL, branch, commitHash string, deployKey []byte) (string, error) {
	var repoLog strings.Builder
	appendLogSection(&repoLog, "Repository sync")

	gitEnv, cleanup, err := e.buildGitEnv(appDir, deployKey)
	if err != nil {
		appendLogLine(&repoLog, "failed to configure git auth environment")
		appendLogLine(&repoLog, err.Error())
		return strings.TrimSpace(repoLog.String()), err
	}
	defer cleanup()

	gitDir := filepath.Join(repoDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		appendLogLine(&repoLog, "repository cache missing; cloning fresh copy")
		cloneArgs := []string{"clone", "--branch", branch}
		if commitHash == "" {
			cloneArgs = append(cloneArgs, "--depth", "1")
		}
		cloneArgs = append(cloneArgs, repoURL, repoDir)
		_, err := e.runCommandWithTranscript(ctx, &repoLog, "git", cloneArgs, appDir, gitEnv, nil)
		if err != nil {
			return strings.TrimSpace(repoLog.String()), err
		}
		if commitHash != "" {
			_, err := e.runCommandWithTranscript(ctx, &repoLog, "git", []string{"checkout", commitHash}, repoDir, gitEnv, nil)
			if err != nil {
				return strings.TrimSpace(repoLog.String()), err
			}
		}
		return strings.TrimSpace(repoLog.String()), nil
	}

	appendLogLine(&repoLog, "repository cache found; fetching latest refs")
	_, err = e.runCommandWithTranscript(ctx, &repoLog, "git", []string{"fetch", "origin"}, repoDir, gitEnv, nil)
	if err != nil {
		return strings.TrimSpace(repoLog.String()), err
	}

	if commitHash != "" {
		_, err := e.runCommandWithTranscript(ctx, &repoLog, "git", []string{"fetch", "origin", commitHash}, repoDir, gitEnv, nil)
		if err != nil {
			return strings.TrimSpace(repoLog.String()), err
		}
		_, err = e.runCommandWithTranscript(ctx, &repoLog, "git", []string{"checkout", commitHash}, repoDir, gitEnv, nil)
		if err != nil {
			return strings.TrimSpace(repoLog.String()), err
		}
		_, err = e.runCommandWithTranscript(ctx, &repoLog, "git", []string{"reset", "--hard", commitHash}, repoDir, gitEnv, nil)
		if err != nil {
			return strings.TrimSpace(repoLog.String()), err
		}
	} else {
		_, err := e.runCommandWithTranscript(ctx, &repoLog, "git", []string{"checkout", branch}, repoDir, gitEnv, nil)
		if err != nil {
			return strings.TrimSpace(repoLog.String()), err
		}
		_, err = e.runCommandWithTranscript(ctx, &repoLog, "git", []string{"reset", "--hard", "origin/" + branch}, repoDir, gitEnv, nil)
		if err != nil {
			return strings.TrimSpace(repoLog.String()), err
		}
	}
	_, err = e.runCommandWithTranscript(ctx, &repoLog, "git", []string{"clean", "-fd"}, repoDir, gitEnv, nil)
	if err != nil {
		return strings.TrimSpace(repoLog.String()), err
	}

	return strings.TrimSpace(repoLog.String()), nil
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

func appendLogSection(builder *strings.Builder, title string) {
	if builder.Len() > 0 {
		builder.WriteString("\n\n")
	}
	builder.WriteString("=== ")
	builder.WriteString(strings.TrimSpace(title))
	builder.WriteString(" ===\n")
}

func appendLogLine(builder *strings.Builder, line string) {
	builder.WriteString(strings.TrimSpace(line))
	builder.WriteString("\n")
}

func appendLogBlock(builder *strings.Builder, block string) {
	block = strings.TrimSpace(block)
	if block == "" {
		return
	}
	if builder.Len() > 0 {
		builder.WriteString("\n\n")
	}
	builder.WriteString(block)
}

func appendCommandOutput(builder *strings.Builder, output string) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		builder.WriteString("(no output)\n")
		return
	}
	builder.WriteString(trimmed)
	builder.WriteString("\n")
}

func formatCommand(cmd string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, cmd)
	for _, arg := range args {
		if strings.ContainsAny(arg, " \t") {
			parts = append(parts, fmt.Sprintf("%q", arg))
			continue
		}
		parts = append(parts, arg)
	}
	return strings.Join(parts, " ")
}

func (e *ComposeExecutor) runCommandWithTranscript(
	ctx context.Context,
	transcript *strings.Builder,
	cmd string,
	args []string,
	workDir string,
	env map[string]string,
	onProgress func(string),
) (string, error) {
	var mu sync.Mutex
	appendToTranscript := func(value string) {
		mu.Lock()
		transcript.WriteString(value)
		mu.Unlock()
	}
	snapshot := func() string {
		mu.Lock()
		value := strings.TrimSpace(transcript.String())
		mu.Unlock()
		return value
	}

	command := formatCommand(cmd, args)
	appendToTranscript("$ " + command + "\n")
	if onProgress != nil {
		onProgress(snapshot())
	}

	sawOutput := false
	output, err := e.runCommand(ctx, cmd, args, workDir, env, func(chunk string) {
		if chunk == "" {
			return
		}
		appendToTranscript(chunk)
		mu.Lock()
		sawOutput = true
		mu.Unlock()
		if onProgress != nil {
			onProgress(snapshot())
		}
	})
	mu.Lock()
	noOutput := !sawOutput
	mu.Unlock()
	if noOutput {
		appendToTranscript("(no output)\n")
		if onProgress != nil {
			onProgress(snapshot())
		}
	}
	if err != nil {
		appendToTranscript("ERROR: " + err.Error() + "\n")
		if onProgress != nil {
			onProgress(snapshot())
		}
	}
	return output, err
}

func (e *ComposeExecutor) runCommand(
	ctx context.Context,
	cmd string,
	args []string,
	workDir string,
	env map[string]string,
	onOutput func(string),
) (string, error) {
	if cmd == "docker" {
		resolution, err := e.resolveDockerCommand(ctx)
		if err != nil {
			return "", err
		}
		cmd = resolution.Path
		env = mergeCommandEnv(env, resolution.Env)
	}

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

	pipeReader, pipeWriter := io.Pipe()
	command.Stdout = pipeWriter
	command.Stderr = pipeWriter

	var outputMu sync.Mutex
	var outputBuilder strings.Builder
	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := pipeReader.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				outputMu.Lock()
				outputBuilder.WriteString(chunk)
				outputMu.Unlock()
				if onOutput != nil {
					onOutput(chunk)
				}
			}

			if readErr != nil {
				if errors.Is(readErr, io.EOF) {
					readDone <- nil
				} else {
					readDone <- readErr
				}
				return
			}
		}
	}()

	if err := command.Start(); err != nil {
		_ = pipeWriter.Close()
		_ = <-readDone
		_ = pipeReader.Close()
		return "", fmt.Errorf("command start failed: %w", err)
	}

	waitErr := command.Wait()
	_ = pipeWriter.Close()
	streamErr := <-readDone
	_ = pipeReader.Close()

	if streamErr != nil && e.Logger != nil {
		e.Logger.Warn("Command output stream failed", "cmd", command.String(), "error", streamErr)
	}

	outputMu.Lock()
	output := outputBuilder.String()
	outputMu.Unlock()
	trimmed := strings.TrimSpace(output)
	if trimmed != "" {
		e.Logger.Info("Command output", "cmd", command.String(), "output", truncateOutput(trimmed))
	}

	if waitErr != nil {
		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
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
		return output, fmt.Errorf("command failed: %w", waitErr)
	}

	e.Logger.Info(
		"Command succeeded",
		"cmd", command.String(),
		"elapsed_ms", time.Since(start).Milliseconds(),
	)

	return output, nil
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

	output, err := e.runCommand(ctx, "docker", args, workDir, nil, nil)
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
