package repoauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	MethodPublic    = "public"
	MethodDeployKey = "deploy_key"

	KnownHostsPathEnv = "CONOPS_KNOWN_HOSTS_FILE"
)

var generatedKnownHosts struct {
	mu   sync.Mutex
	path string
}

var githubFallbackSSHKeys = []string{
	"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl",
	"ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBEmKSENjQEezOmxkZMy7opKgwFB9nkt5YRrYMjNuG5N87uRgg6CLrbo5wAdT/y6v0mKV0U2w0WZ2YB/++Tpockg=",
	"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCj7ndNxQowgcQnjshcLrqPEiiphnt+VTTvDP6mHBL9j1aNUkY4Ue1gvwnGLVlOhGeYrnZaMgRK6+PKCUXaDbC7qtbW8gIkhL7aGCsOr/C56SJMy/BCZfxd1nWzAOxSDPgVsmerOBYfNqltV9/hWCqBywINIR+5dIg6JTJ72pcEpEjcYgXkE2YEFXV1JHnsKgbLWNlhScqb2UmyRkQyytRLtL+38TGxkxCflmO+5Z8CSSNY7GidjMIZ7Q4zMjA2n1nGrlTDkzwDCsw+wqFPGQA179cnfGWOWRVruj16z6XyvxvjJwbz0wQZ75XK5tKSb7FNyeIEs4TT4jk+S4dhPeAUC5y+bDYirYgM4GC7uEnztnZyaVWQ7B381AK4Qdrwt51ZqExKbQpTUNn+EjqoTwvqNj4kqx5QUCI0ThS/YkOxJCXmPUWZbhjpCg56i+2aB6CmK2JGhn57K5mj0MNdBXA4/WnwH6XoPWJzK5Nyu2zB3nAZp+S5hpQs+p1vN1/wsjk=",
}

// NormalizeMethod canonicalizes repo auth methods.
func NormalizeMethod(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "public":
		return MethodPublic
	case "deploy_key", "deploy-key", "deploykey":
		return MethodDeployKey
	default:
		return ""
	}
}

// ValidateCreateInput validates repo config before persistence.
func ValidateCreateInput(repoURL, method, deployKey string) error {
	repoURL = strings.TrimSpace(repoURL)
	if repoURL == "" {
		return fmt.Errorf("repo URL is required")
	}

	method = NormalizeMethod(method)
	if method == "" {
		return fmt.Errorf("unsupported repo auth method")
	}

	if method != MethodDeployKey {
		return nil
	}

	deployKey = NormalizeDeployKey(deployKey)
	if strings.TrimSpace(deployKey) == "" {
		return fmt.Errorf("deploy key is required for private repositories")
	}

	host, err := hostFromRepoURL(repoURL)
	if err != nil {
		return err
	}
	if !strings.EqualFold(host, "github.com") {
		return fmt.Errorf("deploy key mode currently supports only github.com")
	}
	if !isSSHRepoURL(repoURL) {
		return fmt.Errorf("deploy key mode requires an SSH repo URL")
	}

	if _, err := ssh.ParseRawPrivateKey([]byte(deployKey)); err != nil {
		var passErr *ssh.PassphraseMissingError
		if errors.As(err, &passErr) {
			return fmt.Errorf("passphrase-protected deploy keys are not supported")
		}
		return fmt.Errorf("invalid deploy key")
	}

	return nil
}

// NormalizeDeployKey normalizes copy-pasted private keys from forms/JSON payloads.
func NormalizeDeployKey(value string) string {
	normalized := strings.TrimSpace(value)
	normalized = strings.ReplaceAll(normalized, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	// Some API clients send literal "\n" sequences in JSON strings.
	if strings.Contains(normalized, `\n`) && !strings.Contains(normalized, "\n") {
		normalized = strings.ReplaceAll(normalized, `\n`, "\n")
	}
	normalized = strings.TrimSpace(normalized)
	if normalized == "" {
		return ""
	}
	return normalized + "\n"
}

// ResolveKnownHostsPath returns the known_hosts file used for strict host verification.
func ResolveKnownHostsPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv(KnownHostsPathEnv)); path != "" {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
		return "", fmt.Errorf("%s points to an invalid file", KnownHostsPathEnv)
	}

	homeDir, err := os.UserHomeDir()
	if err == nil {
		defaultPath := filepath.Join(homeDir, ".ssh", "known_hosts")
		if info, statErr := os.Stat(defaultPath); statErr == nil && !info.IsDir() {
			return defaultPath, nil
		}
	}

	systemPath := "/etc/ssh/ssh_known_hosts"
	if info, err := os.Stat(systemPath); err == nil && !info.IsDir() {
		return systemPath, nil
	}

	generatedKnownHosts.mu.Lock()
	defer generatedKnownHosts.mu.Unlock()

	if generatedKnownHosts.path != "" {
		if info, err := os.Stat(generatedKnownHosts.path); err == nil && !info.IsDir() {
			return generatedKnownHosts.path, nil
		}
	}

	path, err := bootstrapGitHubKnownHosts()
	if err != nil {
		return "", fmt.Errorf("known_hosts file not found; set %s (%w)", KnownHostsPathEnv, err)
	}
	generatedKnownHosts.path = path
	return path, nil
}

// NewHostKeyCallback returns a strict host-key callback.
func NewHostKeyCallback(path string) (ssh.HostKeyCallback, error) {
	return knownhosts.New(path)
}

func bootstrapGitHubKnownHosts() (string, error) {
	keys, err := fetchGitHubSSHKeys()
	if err != nil || len(keys) == 0 {
		keys = githubFallbackSSHKeys
	}

	for _, key := range keys {
		if _, _, _, _, parseErr := ssh.ParseAuthorizedKey([]byte(key)); parseErr != nil {
			return "", fmt.Errorf("invalid ssh key material for known_hosts generation: %w", parseErr)
		}
	}

	knownHostsDir := filepath.Join(os.TempDir(), "conops-known-hosts")
	if err := os.MkdirAll(knownHostsDir, 0700); err != nil {
		return "", fmt.Errorf("failed creating known_hosts dir: %w", err)
	}

	knownHostsPath := filepath.Join(knownHostsDir, "known_hosts")
	var b strings.Builder
	for _, key := range keys {
		b.WriteString("github.com ")
		b.WriteString(strings.TrimSpace(key))
		b.WriteByte('\n')
	}

	tempPath := knownHostsPath + ".tmp"
	if err := os.WriteFile(tempPath, []byte(b.String()), 0600); err != nil {
		return "", fmt.Errorf("failed writing known_hosts file: %w", err)
	}
	if err := os.Rename(tempPath, knownHostsPath); err != nil {
		return "", fmt.Errorf("failed finalizing known_hosts file: %w", err)
	}

	return knownHostsPath, nil
}

func fetchGitHubSSHKeys() ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/meta", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "conops")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed requesting github metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github metadata request failed with status %d", resp.StatusCode)
	}

	var payload struct {
		SSHKeys []string `json:"ssh_keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed decoding github metadata: %w", err)
	}

	return payload.SSHKeys, nil
}

// BuildSSHCommand builds a strict SSH command for git.
func BuildSSHCommand(keyPath, knownHostsPath string) string {
	return fmt.Sprintf(
		"ssh -i %s -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=%s -F /dev/null",
		shellQuote(keyPath),
		shellQuote(knownHostsPath),
	)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func isSSHRepoURL(repoURL string) bool {
	trimmed := strings.TrimSpace(repoURL)
	if strings.HasPrefix(strings.ToLower(trimmed), "ssh://") {
		return true
	}
	return strings.Contains(trimmed, "@") && strings.Contains(trimmed, ":")
}

func hostFromRepoURL(repoURL string) (string, error) {
	trimmed := strings.TrimSpace(repoURL)
	if trimmed == "" {
		return "", fmt.Errorf("repo URL is required")
	}

	if strings.Contains(trimmed, "://") {
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return "", fmt.Errorf("invalid repo URL: %w", err)
		}
		host := parsed.Hostname()
		if host == "" {
			return "", fmt.Errorf("invalid repo URL host")
		}
		return strings.ToLower(host), nil
	}

	parts := strings.SplitN(trimmed, "@", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid repo URL")
	}

	hostAndPath := parts[1]
	colonIdx := strings.Index(hostAndPath, ":")
	if colonIdx <= 0 {
		return "", fmt.Errorf("invalid SSH repo URL")
	}

	return strings.ToLower(hostAndPath[:colonIdx]), nil
}
