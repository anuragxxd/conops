package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/conops/conops/pkg/agent"
	"github.com/conops/conops/pkg/api"
)

type Agent struct {
	ID            string
	ControllerURL string
	Hostname      string
	client        *http.Client
	logger        *slog.Logger
	executor      *agent.ComposeExecutor
}

func NewAgent(id, controllerURL string) *Agent {
	hostname, _ := os.Hostname()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	return &Agent{
		ID:            id,
		ControllerURL: controllerURL,
		Hostname:      hostname,
		client:        &http.Client{Timeout: 60 * time.Second}, // Longer timeout for compose ops
		logger:        logger,
		executor:      agent.NewComposeExecutor(logger),
	}
}

func (a *Agent) Register() error {
	payload := api.AgentRegistration{
		ID:           a.ID,
		Hostname:     a.Hostname,
		Capabilities: []string{"docker-compose"},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal failed: %w", err)
	}

	resp, err := a.client.Post(a.ControllerURL+"/api/v1/agent/register", "application/json", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registration failed with status: %d", resp.StatusCode)
	}

	a.logger.Info("Agent registered successfully", "id", a.ID)
	return nil
}

func (a *Agent) SendHeartbeat() error {
	payload := api.AgentHeartbeat{
		ID:        a.ID,
		Timestamp: time.Now(),
		Status:    "healthy",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal failed: %w", err)
	}

	resp, err := a.client.Post(a.ControllerURL+"/api/v1/agent/heartbeat", "application/json", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("heartbeat failed with status: %d", resp.StatusCode)
	}

	a.logger.Debug("Heartbeat sent")
	return nil
}

// PollTask checks for new tasks and executes them.
func (a *Agent) PollTask(appID string) error {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/v1/tasks/next?app_id=%s&agent_id=%s", a.ControllerURL, appID, a.ID), nil)
	if err != nil {
		return fmt.Errorf("build poll request failed: %w", err)
	}
	req.Header.Set("X-Agent-ID", a.ID)
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("poll failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return fmt.Errorf("read response failed: %w", readErr)
	}

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("poll returned status: %d body=%s", resp.StatusCode, truncateOutput(string(bodyBytes)))
	}

	var payload struct {
		Data api.SyncTask `json:"data"`
	}
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return fmt.Errorf("decode failed: %w body=%s", err, truncateOutput(string(bodyBytes)))
	}
	task := payload.Data

	a.logger.Info("Received task", "task_id", task.ID)
	if task.ID == "" {
		a.logger.Warn("Task ID is empty")
	}
	a.logger.Info(
		"Task details",
		"task_id", task.ID,
		"compose_bytes", len(task.ComposeContent),
		"env_vars", len(task.EnvVars),
		"repo_url", task.RepoURL,
		"branch", task.Branch,
		"compose_path", task.ComposePath,
	)
	if strings.TrimSpace(task.ComposeContent) == "" {
		a.logger.Warn("Compose content is empty", "task_id", task.ID)
	}

	// Execute task
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	start := time.Now()
	output, err := a.executor.Apply(ctx, appID, task.ComposeContent, task.EnvVars, task.RepoURL, task.Branch, task.ComposePath)
	success := err == nil
	if err != nil {
		a.logger.Error(
			"Task execution failed",
			"task_id", task.ID,
			"error", err,
			"output", truncateOutput(output),
		)
	} else {
		a.logger.Info("Task executed successfully", "task_id", task.ID)
	}
	a.logger.Info(
		"Task execution finished",
		"task_id", task.ID,
		"success", success,
		"elapsed_ms", time.Since(start).Milliseconds(),
	)

	// Report result
	result := api.SyncResult{
		TaskID:    task.ID,
		Success:   success,
		Logs:      output,
		Timestamp: time.Now(),
	}

	return a.SubmitResult(result)
}

func (a *Agent) SubmitResult(result api.SyncResult) error {
	a.logger.Info("Submitting task result", "task_id", result.TaskID, "success", result.Success, "logs_bytes", len(result.Logs))
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, a.ControllerURL+"/api/v1/tasks/result", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("build result request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-ID", a.ID)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("submit result failed: %d", resp.StatusCode)
	}
	a.logger.Info("Task result accepted", "task_id", result.TaskID)
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

func (a *Agent) Run() {
	// Initial Registration Loop
	for {
		if err := a.Register(); err != nil {
			a.logger.Error("Registration failed, retrying...", "error", err)
			time.Sleep(5 * time.Second)
			continue
		}
		break
	}

	// Heartbeat & Task Loop
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// For MVP, we'll hardcode polling for our test app
	// In reality, the agent would know which apps it's responsible for via registration response
	targetAppID := "test-app"

	for range ticker.C {
		// Send Heartbeat
		if err := a.SendHeartbeat(); err != nil {
			a.logger.Error("Failed to send heartbeat", "error", err)
		}

		// Poll for Tasks
		if err := a.PollTask(targetAppID); err != nil {
			// Don't log "poll failed" excessively if it's just connection refused during dev
			// a.logger.Error("Failed to poll tasks", "error", err)
		}
	}
}

func main() {
	controllerURL := os.Getenv("CONOPS_CONTROLLER_URL")
	if controllerURL == "" {
		controllerURL = "http://localhost:8080"
	}

	agentID := os.Getenv("CONOPS_AGENT_ID")
	if agentID == "" {
		agentID = "agent-local-dev"
	}

	agent := NewAgent(agentID, controllerURL)
	agent.logger.Info("Starting agent", "id", agentID, "controller", controllerURL)
	agent.Run()
}
