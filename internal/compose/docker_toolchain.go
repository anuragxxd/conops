package compose

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	dockerResolutionTTL          = 5 * time.Minute
	dockerStaticDownloadHost     = "https://download.docker.com"
	dockerComposeReleasesAPI     = "https://api.github.com/repos/docker/compose/releases/latest"
	dockerVersionProbeFormatJSON = "{{json .}}"
)

type dockerCommandResolution struct {
	Path                string
	Env                 map[string]string
	Source              string
	ClientVersion       string
	ClientAPIVersion    string
	DaemonVersion       string
	DaemonAPIVersion    string
	DaemonMinAPIVersion string
	ComposeVersion      string
}

type dockerPreflightReport struct {
	Resolution dockerCommandResolution
}

func (r dockerPreflightReport) LogLines() []string {
	res := r.Resolution
	lines := []string{
		fmt.Sprintf("docker_command: %s", res.Path),
		fmt.Sprintf("docker_source: %s", res.Source),
		fmt.Sprintf("docker_client_version: %s", fallbackValue(res.ClientVersion)),
		fmt.Sprintf("docker_client_api: %s", fallbackValue(res.ClientAPIVersion)),
		fmt.Sprintf("docker_daemon_version: %s", fallbackValue(res.DaemonVersion)),
		fmt.Sprintf("docker_daemon_api: %s", fallbackValue(res.DaemonAPIVersion)),
		fmt.Sprintf("docker_daemon_min_api: %s", fallbackValue(res.DaemonMinAPIVersion)),
		fmt.Sprintf("docker_compose_version: %s", fallbackValue(res.ComposeVersion)),
	}
	if value := strings.TrimSpace(res.Env["DOCKER_CONFIG"]); value != "" {
		lines = append(lines, fmt.Sprintf("docker_config: %s", value))
	}
	return lines
}

type dockerVersionProbe struct {
	ClientVersion       string
	ClientAPIVersion    string
	ServerVersion       string
	ServerAPIVersion    string
	ServerMinAPIVersion string
	RawOutput           string
}

type dockerVersionJSON struct {
	Client dockerVersionSection `json:"Client"`
	Server dockerVersionSection `json:"Server"`
}

type dockerVersionSection struct {
	Version             string `json:"Version"`
	APIVersion          string `json:"ApiVersion"`
	APIVersionLegacy    string `json:"APIVersion"`
	MinAPIVersion       string `json:"MinAPIVersion"`
	MinAPIVersionLegacy string `json:"MinAPIVERSION"`
}

type composeRelease struct {
	TagName string              `json:"tag_name"`
	Assets  []composeAssetEntry `json:"assets"`
}

type composeAssetEntry struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func (e *ComposeExecutor) ensureDockerPreflight(ctx context.Context) (dockerPreflightReport, error) {
	resolution, err := e.resolveDockerCommand(ctx)
	if err != nil {
		return dockerPreflightReport{}, err
	}
	return dockerPreflightReport{Resolution: resolution}, nil
}

func (e *ComposeExecutor) resolveDockerCommand(ctx context.Context) (dockerCommandResolution, error) {
	e.toolchainMu.Lock()
	defer e.toolchainMu.Unlock()

	if e.dockerResolution.Path != "" && time.Since(e.resolutionAt) < dockerResolutionTTL {
		return e.dockerResolution.clone(), nil
	}

	resolution, err := e.buildDockerResolution(ctx)
	if err != nil {
		return dockerCommandResolution{}, err
	}

	e.dockerResolution = resolution.clone()
	e.resolutionAt = time.Now()
	return resolution, nil
}

func (e *ComposeExecutor) buildDockerResolution(ctx context.Context) (dockerCommandResolution, error) {
	systemProbe, systemErr := probeDockerVersion(ctx, "docker", nil)
	if systemErr == nil {
		compatible, compatErr := dockerAPICompatible(systemProbe.ClientAPIVersion, systemProbe.ServerMinAPIVersion)
		if compatErr != nil {
			return dockerCommandResolution{}, fmt.Errorf("docker api compatibility check failed: %w", compatErr)
		}
		if compatible {
			composeVersion, composeEnv, err := e.ensureComposeAvailable(ctx, "docker")
			if err != nil {
				return dockerCommandResolution{}, err
			}
			return dockerCommandResolution{
				Path:                "docker",
				Env:                 composeEnv,
				Source:              "system",
				ClientVersion:       systemProbe.ClientVersion,
				ClientAPIVersion:    systemProbe.ClientAPIVersion,
				DaemonVersion:       systemProbe.ServerVersion,
				DaemonAPIVersion:    systemProbe.ServerAPIVersion,
				DaemonMinAPIVersion: systemProbe.ServerMinAPIVersion,
				ComposeVersion:      composeVersion,
			}, nil
		}
	}

	managedPath, managedVersion, installErr := e.ensureManagedDockerCLI(ctx)
	if installErr != nil {
		if systemErr != nil {
			return dockerCommandResolution{}, fmt.Errorf("system docker probe failed (%v) and managed docker install failed: %w", systemErr, installErr)
		}
		return dockerCommandResolution{}, fmt.Errorf("system docker client api %s is below daemon minimum %s and managed docker install failed: %w", fallbackValue(systemProbe.ClientAPIVersion), fallbackValue(systemProbe.ServerMinAPIVersion), installErr)
	}

	managedProbe, managedErr := probeDockerVersion(ctx, managedPath, nil)
	if managedErr != nil {
		if systemErr != nil {
			return dockerCommandResolution{}, fmt.Errorf("system docker probe failed (%v) and managed docker probe failed (%v)", systemErr, managedErr)
		}
		return dockerCommandResolution{}, fmt.Errorf("managed docker probe failed: %w", managedErr)
	}

	compatible, compatErr := dockerAPICompatible(managedProbe.ClientAPIVersion, managedProbe.ServerMinAPIVersion)
	if compatErr != nil {
		return dockerCommandResolution{}, fmt.Errorf("managed docker api compatibility check failed: %w", compatErr)
	}
	if !compatible {
		return dockerCommandResolution{}, fmt.Errorf(
			"managed docker client api %s is older than daemon minimum %s (managed_cli=%s)",
			fallbackValue(managedProbe.ClientAPIVersion),
			fallbackValue(managedProbe.ServerMinAPIVersion),
			managedVersion,
		)
	}

	composeVersion, composeEnv, composeErr := e.ensureComposeAvailable(ctx, managedPath)
	if composeErr != nil {
		return dockerCommandResolution{}, composeErr
	}

	return dockerCommandResolution{
		Path:                managedPath,
		Env:                 composeEnv,
		Source:              "managed:" + managedVersion,
		ClientVersion:       managedProbe.ClientVersion,
		ClientAPIVersion:    managedProbe.ClientAPIVersion,
		DaemonVersion:       managedProbe.ServerVersion,
		DaemonAPIVersion:    managedProbe.ServerAPIVersion,
		DaemonMinAPIVersion: managedProbe.ServerMinAPIVersion,
		ComposeVersion:      composeVersion,
	}, nil
}

func (e *ComposeExecutor) ensureComposeAvailable(ctx context.Context, dockerPath string) (string, map[string]string, error) {
	version, versionErr := probeComposeVersion(ctx, dockerPath, nil)
	if versionErr == nil {
		daemonErr := probeComposeDaemonCompatibility(ctx, dockerPath, nil)
		if daemonErr == nil {
			return version, nil, nil
		}
		if !looksLikeDockerAPIMismatch(daemonErr) {
			return "", nil, fmt.Errorf("docker compose daemon compatibility check failed: %w", daemonErr)
		}
	}

	installEnv, installErr := e.installComposePlugin(ctx, true)
	if installErr != nil {
		if versionErr != nil {
			return "", nil, fmt.Errorf("docker compose plugin unavailable and install failed: %w", installErr)
		}
		return "", nil, fmt.Errorf("docker compose plugin is incompatible with daemon and refresh failed: %w", installErr)
	}

	version, versionErr = probeComposeVersion(ctx, dockerPath, installEnv)
	if versionErr != nil {
		return "", nil, fmt.Errorf("docker compose plugin check failed after install: %w", versionErr)
	}
	if daemonErr := probeComposeDaemonCompatibility(ctx, dockerPath, installEnv); daemonErr != nil {
		return "", nil, fmt.Errorf("docker compose daemon compatibility check failed after install: %w", daemonErr)
	}
	return version, installEnv, nil
}

func (e *ComposeExecutor) ensureManagedDockerCLI(ctx context.Context) (string, string, error) {
	if value := strings.TrimSpace(os.Getenv("CONOPS_DOCKER_CLI_PATH")); value != "" {
		if _, err := os.Stat(value); err != nil {
			return "", "", fmt.Errorf("CONOPS_DOCKER_CLI_PATH does not exist: %w", err)
		}
		return value, "custom", nil
	}

	platformPath, err := dockerStaticPlatformPath(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", "", err
	}
	baseURL := fmt.Sprintf("%s/%s/", dockerStaticDownloadHost, platformPath)

	candidateVersions := []string{}
	if override := strings.TrimSpace(os.Getenv("CONOPS_DOCKER_CLI_VERSION")); override != "" {
		candidateVersions = append(candidateVersions, override)
	} else {
		versions, fetchErr := fetchDockerStaticVersions(ctx, baseURL)
		if fetchErr != nil {
			return "", "", fmt.Errorf("failed to fetch docker static versions from %s: %w", baseURL, fetchErr)
		}
		if len(versions) == 0 {
			return "", "", fmt.Errorf("no docker static versions found at %s", baseURL)
		}
		limit := min(5, len(versions))
		for i := len(versions) - 1; i >= len(versions)-limit; i-- {
			candidateVersions = append(candidateVersions, versions[i])
		}
	}

	toolsRoot, err := e.toolsRootDir()
	if err != nil {
		return "", "", err
	}

	var installErrors []string
	for _, version := range candidateVersions {
		binaryPath, installErr := installDockerBinaryVersion(ctx, baseURL, version, toolsRoot)
		if installErr != nil {
			installErrors = append(installErrors, fmt.Sprintf("%s: %v", version, installErr))
			continue
		}
		return binaryPath, version, nil
	}

	if len(installErrors) == 0 {
		return "", "", fmt.Errorf("no candidate docker versions available")
	}
	return "", "", fmt.Errorf("failed to install managed docker cli (%s)", strings.Join(installErrors, "; "))
}

func (e *ComposeExecutor) installComposePlugin(ctx context.Context, force bool) (map[string]string, error) {
	pluginDir, envVars, err := e.resolveComposePluginDirectory()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		return nil, fmt.Errorf("create compose plugin directory failed: %w", err)
	}

	pluginName := "docker-compose"
	if runtime.GOOS == "windows" {
		pluginName += ".exe"
	}
	pluginPath := filepath.Join(pluginDir, pluginName)
	if stat, statErr := os.Stat(pluginPath); statErr == nil && !stat.IsDir() && !force {
		return envVars, nil
	}
	if force {
		_ = os.Remove(pluginPath)
	}

	tagName := strings.TrimSpace(os.Getenv("CONOPS_COMPOSE_PLUGIN_VERSION"))
	assetName, err := composeAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, err
	}

	binaryURL := ""
	checksumsURL := ""
	if tagName != "" {
		if !strings.HasPrefix(tagName, "v") {
			tagName = "v" + tagName
		}
		binaryURL = fmt.Sprintf("https://github.com/docker/compose/releases/download/%s/%s", tagName, assetName)
		checksumsURL = fmt.Sprintf("https://github.com/docker/compose/releases/download/%s/checksums.txt", tagName)
	} else {
		release, releaseErr := fetchLatestComposeRelease(ctx)
		if releaseErr != nil {
			return nil, releaseErr
		}
		if strings.TrimSpace(release.TagName) == "" {
			return nil, fmt.Errorf("latest compose release did not include a tag")
		}
		for _, asset := range release.Assets {
			switch asset.Name {
			case assetName:
				binaryURL = asset.BrowserDownloadURL
			case "checksums.txt":
				checksumsURL = asset.BrowserDownloadURL
			}
		}
		if binaryURL == "" {
			binaryURL = fmt.Sprintf("https://github.com/docker/compose/releases/download/%s/%s", release.TagName, assetName)
		}
		if checksumsURL == "" {
			checksumsURL = fmt.Sprintf("https://github.com/docker/compose/releases/download/%s/checksums.txt", release.TagName)
		}
	}

	tmpPath := pluginPath + ".tmp"
	shaHex, err := downloadFile(ctx, binaryURL, tmpPath, 0755)
	if err != nil {
		return nil, fmt.Errorf("download compose plugin failed: %w", err)
	}
	if err := verifyChecksumFromURL(ctx, checksumsURL, assetName, shaHex); err != nil {
		_ = os.Remove(tmpPath)
		return nil, err
	}

	if err := os.Rename(tmpPath, pluginPath); err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("install compose plugin failed: %w", err)
	}
	if err := os.Chmod(pluginPath, 0755); err != nil {
		return nil, fmt.Errorf("set compose plugin executable bit failed: %w", err)
	}

	return envVars, nil
}

func (e *ComposeExecutor) resolveComposePluginDirectory() (string, map[string]string, error) {
	if configDir := strings.TrimSpace(os.Getenv("DOCKER_CONFIG")); configDir != "" {
		return filepath.Join(configDir, "cli-plugins"), nil, nil
	}

	homeDir, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(homeDir) != "" {
		defaultDir := filepath.Join(homeDir, ".docker", "cli-plugins")
		if mkdirErr := os.MkdirAll(defaultDir, 0755); mkdirErr == nil {
			return defaultDir, nil, nil
		}
	}

	toolsRoot, err := e.toolsRootDir()
	if err != nil {
		return "", nil, err
	}
	configDir := filepath.Join(toolsRoot, "docker-config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", nil, fmt.Errorf("create docker config fallback failed: %w", err)
	}
	return filepath.Join(configDir, "cli-plugins"), map[string]string{"DOCKER_CONFIG": configDir}, nil
}

func (e *ComposeExecutor) toolsRootDir() (string, error) {
	root := strings.TrimSpace(e.ToolsDir)
	if root == "" {
		root = "./.conops-tools"
	}
	absolutePath, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve tools dir failed: %w", err)
	}
	if err := os.MkdirAll(absolutePath, 0755); err != nil {
		return "", fmt.Errorf("create tools dir failed: %w", err)
	}
	return absolutePath, nil
}

func installDockerBinaryVersion(ctx context.Context, baseURL, version, toolsRoot string) (string, error) {
	installDir := filepath.Join(toolsRoot, "docker-cli", version)
	binaryName := "docker"
	if runtime.GOOS == "windows" {
		binaryName = "docker.exe"
	}
	binaryPath := filepath.Join(installDir, binaryName)

	if stat, err := os.Stat(binaryPath); err == nil && !stat.IsDir() {
		return binaryPath, nil
	}

	if err := os.MkdirAll(installDir, 0755); err != nil {
		return "", fmt.Errorf("create managed docker directory failed: %w", err)
	}

	archiveURL := fmt.Sprintf("%sdocker-%s.tgz", baseURL, version)
	if err := downloadAndExtractDockerBinary(ctx, archiveURL, binaryPath); err != nil {
		return "", err
	}
	return binaryPath, nil
}

func downloadAndExtractDockerBinary(ctx context.Context, archiveURL, binaryPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("download %s failed with status %s", archiveURL, resp.Status)
	}

	gzReader, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("open docker archive failed: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	tmpPath := binaryPath + ".tmp"
	_ = os.Remove(tmpPath)

	found := false
	for {
		header, err := tarReader.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read docker archive failed: %w", err)
		}

		if header.FileInfo().IsDir() {
			continue
		}
		name := filepath.Base(header.Name)
		if name != "docker" && name != "docker.exe" {
			continue
		}

		file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			return fmt.Errorf("create docker binary temp file failed: %w", err)
		}
		if _, err := io.Copy(file, tarReader); err != nil {
			_ = file.Close()
			_ = os.Remove(tmpPath)
			return fmt.Errorf("extract docker binary failed: %w", err)
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(tmpPath)
			return err
		}
		found = true
		break
	}

	if !found {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("docker archive did not include docker binary")
	}

	if err := os.Rename(tmpPath, binaryPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("install docker binary failed: %w", err)
	}
	if err := os.Chmod(binaryPath, 0755); err != nil {
		return fmt.Errorf("set docker binary executable bit failed: %w", err)
	}
	return nil
}

func fetchDockerStaticVersions(ctx context.Context, baseURL string) ([]string, error) {
	body, err := fetchText(ctx, baseURL)
	if err != nil {
		return nil, err
	}

	re := regexp.MustCompile(`docker-([0-9]+\.[0-9]+\.[0-9]+)\.tgz`)
	matches := re.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil, nil
	}

	unique := make(map[string]struct{}, len(matches))
	versions := make([]string, 0, len(matches))
	for _, match := range matches {
		version := strings.TrimSpace(match[1])
		if version == "" {
			continue
		}
		if _, exists := unique[version]; exists {
			continue
		}
		unique[version] = struct{}{}
		versions = append(versions, version)
	}

	slices.SortFunc(versions, compareSemverStrings)
	return versions, nil
}

func probeDockerVersion(ctx context.Context, dockerPath string, envVars map[string]string) (dockerVersionProbe, error) {
	output, err := runCommandCapture(ctx, dockerPath, []string{"version", "--format", dockerVersionProbeFormatJSON}, envVars)
	if err != nil {
		return dockerVersionProbe{RawOutput: output}, fmt.Errorf("%w: %s", err, strings.TrimSpace(output))
	}

	parsed := dockerVersionJSON{}
	if decodeErr := json.Unmarshal([]byte(strings.TrimSpace(output)), &parsed); decodeErr != nil {
		return dockerVersionProbe{RawOutput: output}, fmt.Errorf("parse docker version output failed: %w", decodeErr)
	}

	clientAPIVersion := strings.TrimSpace(parsed.Client.APIVersion)
	if clientAPIVersion == "" {
		clientAPIVersion = strings.TrimSpace(parsed.Client.APIVersionLegacy)
	}
	serverMinAPIVersion := strings.TrimSpace(parsed.Server.MinAPIVersion)
	if serverMinAPIVersion == "" {
		serverMinAPIVersion = strings.TrimSpace(parsed.Server.MinAPIVersionLegacy)
	}

	return dockerVersionProbe{
		ClientVersion:       strings.TrimSpace(parsed.Client.Version),
		ClientAPIVersion:    clientAPIVersion,
		ServerVersion:       strings.TrimSpace(parsed.Server.Version),
		ServerAPIVersion:    strings.TrimSpace(parsed.Server.APIVersion),
		ServerMinAPIVersion: serverMinAPIVersion,
		RawOutput:           output,
	}, nil
}

func probeComposeVersion(ctx context.Context, dockerPath string, envVars map[string]string) (string, error) {
	output, err := runCommandCapture(ctx, dockerPath, []string{"compose", "version", "--short"}, envVars)
	if err == nil {
		version := strings.TrimSpace(output)
		if version != "" {
			return version, nil
		}
	}

	output, err = runCommandCapture(ctx, dockerPath, []string{"compose", "version"}, envVars)
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(output))
	}
	line := strings.TrimSpace(output)
	if line == "" {
		return "", fmt.Errorf("docker compose version returned empty output")
	}
	parts := strings.Fields(line)
	if len(parts) > 0 {
		return parts[len(parts)-1], nil
	}
	return line, nil
}

func probeComposeDaemonCompatibility(ctx context.Context, dockerPath string, envVars map[string]string) error {
	output, err := runCommandCapture(ctx, dockerPath, []string{"compose", "ls", "--all"}, envVars)
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(output))
	}
	// Some compose versions exit 0 but print API mismatch warnings to
	// combined output. Catch those so we can trigger a plugin upgrade.
	if outputLooksLikeDockerAPIMismatch(output) {
		return fmt.Errorf("compose plugin reported API mismatch: %s", strings.TrimSpace(output))
	}
	return nil
}

func fetchLatestComposeRelease(ctx context.Context) (composeRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dockerComposeReleasesAPI, nil)
	if err != nil {
		return composeRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return composeRelease{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return composeRelease{}, fmt.Errorf("compose release lookup failed with status %s", resp.Status)
	}

	var release composeRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return composeRelease{}, err
	}
	return release, nil
}

func verifyChecksumFromURL(ctx context.Context, checksumsURL, assetName, actualSHA string) error {
	if checksumsURL == "" {
		return nil
	}
	checksums, err := fetchText(ctx, checksumsURL)
	if err != nil {
		return fmt.Errorf("fetch compose checksums failed: %w", err)
	}

	expectedSHA := ""
	for _, line := range strings.Split(checksums, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == assetName {
			expectedSHA = strings.TrimSpace(fields[0])
			break
		}
	}

	if expectedSHA == "" {
		return fmt.Errorf("compose checksum for %s not found", assetName)
	}
	if !strings.EqualFold(expectedSHA, actualSHA) {
		return fmt.Errorf("compose checksum mismatch for %s", assetName)
	}
	return nil
}

func downloadFile(ctx context.Context, url, destPath string, mode os.FileMode) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("download %s failed with status %s", url, resp.Status)
	}

	file, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(file, hasher), resp.Body); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func runCommandCapture(ctx context.Context, cmd string, args []string, envVars map[string]string) (string, error) {
	command := exec.CommandContext(ctx, cmd, args...)
	if len(envVars) > 0 {
		merged := append([]string{}, os.Environ()...)
		for key, value := range envVars {
			merged = append(merged, fmt.Sprintf("%s=%s", key, value))
		}
		command.Env = merged
	}

	output, err := command.CombinedOutput()
	return string(output), err
}

func fetchText(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("request to %s failed with status %s", url, resp.Status)
	}

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func composeAssetName(goos, goarch string) (string, error) {
	arch := ""
	switch goarch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	default:
		return "", fmt.Errorf("unsupported compose plugin architecture: %s", goarch)
	}

	switch goos {
	case "linux", "darwin", "windows":
		return fmt.Sprintf("docker-compose-%s-%s", goos, arch), nil
	default:
		return "", fmt.Errorf("unsupported compose plugin os: %s", goos)
	}
}

func dockerStaticPlatformPath(goos, goarch string) (string, error) {
	arch := ""
	switch goarch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	default:
		return "", fmt.Errorf("unsupported docker static architecture: %s", goarch)
	}

	switch goos {
	case "linux":
		return fmt.Sprintf("linux/static/stable/%s", arch), nil
	case "darwin":
		return fmt.Sprintf("mac/static/stable/%s", arch), nil
	default:
		return "", fmt.Errorf("unsupported docker static os: %s", goos)
	}
}

func dockerAPICompatible(clientAPI, minServerAPI string) (bool, error) {
	clientAPI = strings.TrimSpace(clientAPI)
	minServerAPI = strings.TrimSpace(minServerAPI)
	if minServerAPI == "" {
		return true, nil
	}
	if clientAPI == "" {
		return false, fmt.Errorf("client API version is empty while daemon minimum API is %s", minServerAPI)
	}
	comparison, err := compareNumericVersions(clientAPI, minServerAPI)
	if err != nil {
		return false, err
	}
	return comparison >= 0, nil
}

func compareNumericVersions(a, b string) (int, error) {
	aParts, err := parseNumericVersionParts(a)
	if err != nil {
		return 0, fmt.Errorf("invalid version %q: %w", a, err)
	}
	bParts, err := parseNumericVersionParts(b)
	if err != nil {
		return 0, fmt.Errorf("invalid version %q: %w", b, err)
	}

	maxLen := len(aParts)
	if len(bParts) > maxLen {
		maxLen = len(bParts)
	}
	for i := 0; i < maxLen; i++ {
		aValue := 0
		if i < len(aParts) {
			aValue = aParts[i]
		}
		bValue := 0
		if i < len(bParts) {
			bValue = bParts[i]
		}
		if aValue < bValue {
			return -1, nil
		}
		if aValue > bValue {
			return 1, nil
		}
	}
	return 0, nil
}

func compareSemverStrings(a, b string) int {
	cmp, err := compareNumericVersions(a, b)
	if err != nil {
		return strings.Compare(a, b)
	}
	return cmp
}

func parseNumericVersionParts(value string) ([]int, error) {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimPrefix(trimmed, "v")
	if trimmed == "" {
		return nil, fmt.Errorf("empty value")
	}

	parts := strings.Split(trimmed, ".")
	parsed := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty version segment")
		}
		if idx := strings.IndexByte(part, '-'); idx >= 0 {
			part = part[:idx]
		}
		if part == "" {
			return nil, fmt.Errorf("invalid version segment")
		}
		number, err := strconv.Atoi(part)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, number)
	}
	return parsed, nil
}

func (r dockerCommandResolution) clone() dockerCommandResolution {
	copyEnv := map[string]string(nil)
	if len(r.Env) > 0 {
		copyEnv = make(map[string]string, len(r.Env))
		for key, value := range r.Env {
			copyEnv[key] = value
		}
	}
	r.Env = copyEnv
	return r
}

func fallbackValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "unknown"
	}
	return trimmed
}

func looksLikeDockerAPIMismatch(err error) bool {
	if err == nil {
		return false
	}
	return outputLooksLikeDockerAPIMismatch(err.Error())
}

func outputLooksLikeDockerAPIMismatch(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "client version") &&
		strings.Contains(lower, "minimum supported api version")
}

// forceRefreshComposePlugin installs the latest compose plugin and invalidates
// the cached docker resolution so the next command picks it up.
func (e *ComposeExecutor) forceRefreshComposePlugin(ctx context.Context) error {
	e.toolchainMu.Lock()
	e.dockerResolution = dockerCommandResolution{}
	e.resolutionAt = time.Time{}
	e.toolchainMu.Unlock()

	_, err := e.installComposePlugin(ctx, true)
	return err
}

func mergeCommandEnv(base, extra map[string]string) map[string]string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	merged := make(map[string]string, len(base)+len(extra))
	for key, value := range extra {
		merged[key] = value
	}
	for key, value := range base {
		merged[key] = value
	}
	return merged
}
