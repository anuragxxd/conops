package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/conops/conops/internal/api"
	"github.com/conops/conops/internal/credentials"
	"github.com/conops/conops/internal/repoauth"
	"github.com/conops/conops/internal/store"
	"github.com/google/uuid"
)

// App is aliased to api.App for compatibility, though we should prefer api.App.
type App = api.App

// Registry manages the lifecycle of tracked applications using a backend store.
type Registry struct {
	store       store.Store
	credentials *credentials.Service
}

// NewRegistry creates a new application registry with the given store backend.
func NewRegistry(s store.Store, credentialService *credentials.Service) *Registry {
	return &Registry{
		store:       s,
		credentials: credentialService,
	}
}

// Add registers a new application.
func (r *Registry) Add(app *api.App) error {
	return r.AddWithDeployKey(app, "")
}

// AddWithDeployKeyAndEnvs registers a new application with optional deploy key and environment variables.
func (r *Registry) AddWithDeployKeyAndEnvs(app *api.App, deployKey string, serviceEnvs map[string]string) error {
	if app == nil {
		return fmt.Errorf("app is required")
	}

	deployKey = repoauth.NormalizeDeployKey(deployKey)
	if strings.TrimSpace(app.ID) == "" {
		app.ID = uuid.NewString()
	}

	if normalized := repoauth.NormalizeMethod(app.RepoAuthMethod); normalized != "" {
		app.RepoAuthMethod = normalized
	} else if deployKey != "" {
		app.RepoAuthMethod = repoauth.MethodDeployKey
	} else {
		app.RepoAuthMethod = repoauth.MethodPublic
	}

	if err := repoauth.ValidateCreateInput(app.RepoURL, app.RepoAuthMethod, deployKey); err != nil {
		return err
	}

	// Check encryption support if sensitive data is provided
	hasDeployKey := app.RepoAuthMethod == repoauth.MethodDeployKey
	hasEnvVars := len(serviceEnvs) > 0

	if (hasDeployKey || hasEnvVars) && (r.credentials == nil || !r.credentials.Enabled()) {
		return fmt.Errorf("encryption support is unavailable: set %s", credentials.EncryptionKeyEnv)
	}

	// Set defaults if missing
	if app.Branch == "" {
		app.Branch = "main"
	}
	if app.ComposePath == "" {
		app.ComposePath = "compose.yaml"
	}
	if app.PollInterval == "" {
		app.PollInterval = "30s"
	}
	// New apps should enter the reconciliation pipeline immediately.
	app.Status = "pending"
	app.LastSyncAt = time.Time{}

	if err := r.store.CreateApp(context.Background(), app); err != nil {
		return err
	}

	// If no credentials to store, return early
	if !hasDeployKey && !hasEnvVars {
		return nil
	}

	cred := &store.AppCredential{
		AppID: app.ID,
	}

	if hasDeployKey {
		plaintext := []byte(deployKey)
		defer zeroBytes(plaintext)

		ciphertext, nonce, err := r.credentials.Encrypt(plaintext)
		if err != nil {
			_ = r.store.DeleteApp(context.Background(), app.ID)
			return err
		}
		cred.DeployKeyCiphertext = ciphertext
		cred.DeployKeyNonce = nonce
	}

	if hasEnvVars {
		// Serialize map to JSON before encryption
		// We use standard JSON marshalling
		jsonBytes, err := json.Marshal(serviceEnvs)
		if err != nil {
			_ = r.store.DeleteApp(context.Background(), app.ID)
			return fmt.Errorf("failed to serialize env vars: %w", err)
		}
		defer zeroBytes(jsonBytes)

		ciphertext, nonce, err := r.credentials.Encrypt(jsonBytes)
		if err != nil {
			_ = r.store.DeleteApp(context.Background(), app.ID)
			return err
		}
		cred.EnvCiphertext = ciphertext
		cred.EnvNonce = nonce
	}

	if err := r.store.UpsertAppCredential(context.Background(), cred); err != nil {
		_ = r.store.DeleteApp(context.Background(), app.ID)
		return err
	}

	return nil
}

// AddWithDeployKey registers a new application and stores deploy-key credentials when provided.
func (r *Registry) AddWithDeployKey(app *api.App, deployKey string) error {
	return r.AddWithDeployKeyAndEnvs(app, deployKey, nil)
}

// Get retrieves an application by ID.
func (r *Registry) Get(id string) (*api.App, error) {
	return r.store.GetApp(context.Background(), id)
}

// List returns all registered applications.
func (r *Registry) List() []*api.App {
	apps, err := r.store.ListApps(context.Background())
	if err != nil {
		// Log error? For now return empty list to be safe for UI.
		return []*api.App{}
	}
	return apps
}

// Delete removes an application by ID.
func (r *Registry) Delete(id string) error {
	if err := r.store.DeleteAppCredential(context.Background(), id); err != nil {
		return err
	}
	return r.store.DeleteApp(context.Background(), id)
}

// UpdateApp updates an application's editable fields and optionally its environment variables.
func (r *Registry) UpdateApp(id, name, branch, composePath, pollInterval string, serviceEnvs map[string]string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("app id is required")
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("app name is required")
	}
	if strings.TrimSpace(branch) == "" {
		return fmt.Errorf("branch is required")
	}
	if strings.TrimSpace(composePath) == "" {
		return fmt.Errorf("compose path is required")
	}
	if strings.TrimSpace(pollInterval) == "" {
		return fmt.Errorf("poll interval is required")
	}

	// Verify app exists
	if _, err := r.store.GetApp(context.Background(), id); err != nil {
		return fmt.Errorf("app not found: %w", err)
	}

	// Update app fields
	if err := r.store.UpdateApp(context.Background(), id, name, branch, composePath, pollInterval); err != nil {
		return fmt.Errorf("failed to update app: %w", err)
	}

	// Update environment variables if provided
	if serviceEnvs != nil {
		hasEnvVars := len(serviceEnvs) > 0

		if hasEnvVars && (r.credentials == nil || !r.credentials.Enabled()) {
			return fmt.Errorf("encryption support is unavailable: set %s", credentials.EncryptionKeyEnv)
		}

		if hasEnvVars {
			// Serialize and encrypt env vars
			jsonBytes, err := json.Marshal(serviceEnvs)
			if err != nil {
				return fmt.Errorf("failed to serialize env vars: %w", err)
			}
			defer zeroBytes(jsonBytes)

			ciphertext, nonce, err := r.credentials.Encrypt(jsonBytes)
			if err != nil {
				return fmt.Errorf("failed to encrypt env vars: %w", err)
			}

			// Check if credentials exist
			_, err = r.store.GetAppCredential(context.Background(), id)
			if err != nil {
				if errors.Is(err, store.ErrCredentialNotFound) {
					// Create new credential entry with only env vars
					cred := &store.AppCredential{
						AppID:         id,
						EnvCiphertext: ciphertext,
						EnvNonce:      nonce,
					}
					if err := r.store.UpsertAppCredential(context.Background(), cred); err != nil {
						return fmt.Errorf("failed to create app credentials: %w", err)
					}
				} else {
					return fmt.Errorf("failed to check credentials: %w", err)
				}
			} else {
				// Update existing credentials
				if err := r.store.UpdateAppCredentials(context.Background(), id, ciphertext, nonce); err != nil {
					return fmt.Errorf("failed to update app credentials: %w", err)
				}
			}
		} else {
			// Clear env vars by updating to empty
			_ = r.store.UpdateAppCredentials(context.Background(), id, nil, nil)
		}
	}

	return nil
}

// GetAppEnvs returns the decrypted environment variables for the app.
func (r *Registry) GetAppEnvs(id string) (map[string]string, error) {
	credential, err := r.store.GetAppCredential(context.Background(), id)
	if err != nil {
		if errors.Is(err, store.ErrCredentialNotFound) {
			return nil, nil
		}
		return nil, err
	}

	if len(credential.EnvCiphertext) == 0 {
		return nil, nil
	}

	if r.credentials == nil || !r.credentials.Enabled() {
		return nil, fmt.Errorf("encryption support is unavailable: set %s", credentials.EncryptionKeyEnv)
	}

	jsonBytes, err := r.credentials.Decrypt(credential.EnvCiphertext, credential.EnvNonce)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt env vars: %w", err)
	}
	defer zeroBytes(jsonBytes)

	var envs map[string]string
	if err := json.Unmarshal(jsonBytes, &envs); err != nil {
		return nil, fmt.Errorf("failed to deserialize env vars: %w", err)
	}

	return envs, nil
}

// GetDeployKey returns the decrypted deploy key for the app if configured.
func (r *Registry) GetDeployKey(id string) ([]byte, error) {
	credential, err := r.store.GetAppCredential(context.Background(), id)
	if err != nil {
		if errors.Is(err, store.ErrCredentialNotFound) {
			return nil, nil
		}
		return nil, err
	}

	if len(credential.DeployKeyCiphertext) == 0 {
		return nil, nil
	}

	if r.credentials == nil || !r.credentials.Enabled() {
		return nil, fmt.Errorf("deploy key support is unavailable: set %s", credentials.EncryptionKeyEnv)
	}

	deployKey, err := r.credentials.Decrypt(credential.DeployKeyCiphertext, credential.DeployKeyNonce)
	if err != nil {
		return nil, err
	}

	normalized := []byte(repoauth.NormalizeDeployKey(string(deployKey)))
	zeroBytes(deployKey)
	return normalized, nil
}

// UpdateCommit updates the latest commit hash for an app.
func (r *Registry) UpdateCommit(id, commitHash string) error {
	return r.UpdateCommitWithMessage(id, commitHash, "")
}

// UpdateCommitWithMessage updates latest desired commit hash and subject.
func (r *Registry) UpdateCommitWithMessage(id, commitHash, commitMessage string) error {
	return r.store.UpdateAppCommit(context.Background(), id, commitHash, commitMessage)
}

// UpdateStatus updates app status and optionally the last sync time.
func (r *Registry) UpdateStatus(id, status string, lastSyncAt *time.Time) error {
	return r.store.UpdateAppStatus(context.Background(), id, status, lastSyncAt)
}

// UpdateSyncResult stores sync execution metadata.
func (r *Registry) UpdateSyncResult(
	id string,
	status string,
	lastSyncAt time.Time,
	syncedCommit string,
	syncedCommitMessage string,
	syncOutput string,
	syncError string,
) error {
	return r.store.UpdateAppSyncResult(
		context.Background(),
		id,
		status,
		lastSyncAt,
		syncedCommit,
		syncedCommitMessage,
		syncOutput,
		syncError,
	)
}

// UpdateSyncProgress stores in-flight sync logs while status is syncing.
func (r *Registry) UpdateSyncProgress(id string, lastSyncAt time.Time, syncOutput string) error {
	return r.store.UpdateAppSyncProgress(context.Background(), id, lastSyncAt, syncOutput)
}

func zeroBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
