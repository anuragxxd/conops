package ui

import (
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/conops/conops/internal/compose"
	"github.com/conops/conops/internal/controller"
	"github.com/go-chi/chi/v5"
)

// Handler manages UI requests.
type Handler struct {
	Registry *controller.Registry
	Executor *compose.ComposeExecutor
	Tmpl     *template.Template
}

// ServiceView is the view model for a container in the detail page.
type ServiceView struct {
	Service string
	Image   string
	Status  string // "running" or "exited"
	Health  string // "healthy", "unhealthy", "starting", or ""
	Ports   string
}

// AppView is the view model for an app in the list.
type AppView struct {
	ID                  string
	Name                string
	RepoURL             string
	RepoShort           string // e.g. "org/repo" extracted from full URL
	Branch              string
	Status              string
	LastSyncAt          string
	LastSyncAtRelative  string
	SyncedCommitShort   string
	SyncedCommitMessage string
	InSync              bool
}

// AppDetailView is the view model for the app detail page.
type AppDetailView struct {
	ID                      string
	Name                    string
	RepoURL                 string
	RepoAuth                string
	Branch                  string
	ComposePath             string
	PollInterval            string
	LastSeenCommit          string
	LastSeenCommitMessage   string
	LastSeenCommitShort     string
	LastSyncedCommit        string
	LastSyncedCommitMessage string
	LastSyncedCommitShort   string
	LastSyncOutput          string
	LastSyncError           string
	Status                  string
	LastSyncAt              string
	LastSyncAtRelative      string

	// Runtime container information
	Services       []ServiceView
	ContainerCount int
	RunningCount   int
	HealthLabel    string // "Healthy", "Degraded", "Down", "No data"
	InSync         bool   // true when desired commit == synced commit
}

// AppFormData is the view model for the new app form.
type AppFormData struct {
	Name        string
	RepoURL     string
	RepoAuth    string
	DeployKey   string
	Branch      string
	ComposePath string
}

// AppsPageData is the data passed to the apps page template.
type AppsPageData struct {
	Page  string
	Apps  []AppView
	App   AppDetailView
	Form  AppFormData
	Error string
}

// NewHandler creates a new UI handler.
func NewHandler(registry *controller.Registry, executor *compose.ComposeExecutor, templateDir string) (*Handler, error) {
	tmpl, err := template.ParseGlob(filepath.Join(templateDir, "*.html"))
	if err != nil {
		return nil, err
	}

	return &Handler{
		Registry: registry,
		Executor: executor,
		Tmpl:     tmpl,
	}, nil
}

// ServeAppsPage handles the main apps list page request.
func (h *Handler) ServeAppsPage(w http.ResponseWriter, r *http.Request) {
	data := AppsPageData{
		Page: "list",
	}
	if err := h.Tmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ServeNewAppPage handles the dedicated app creation page.
func (h *Handler) ServeNewAppPage(w http.ResponseWriter, r *http.Request) {
	data := AppsPageData{
		Page: "new",
		Form: AppFormData{
			RepoAuth:    "public",
			Branch:      "main",
			ComposePath: "compose.yaml",
		},
	}
	if err := h.Tmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ServeAppDetailPage handles the app detail page.
func (h *Handler) ServeAppDetailPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	detail, err := h.loadAppDetail(r, id)
	if err != nil {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	data := AppsPageData{
		Page: "detail",
		App:  detail,
	}
	if err := h.Tmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ServeAppDetailFragment handles HTMX refresh requests for app details.
func (h *Handler) ServeAppDetailFragment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	detail, err := h.loadAppDetail(r, id)
	if err != nil {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	data := AppsPageData{
		Page: "detail",
		App:  detail,
	}
	if err := h.Tmpl.ExecuteTemplate(w, "app-detail-live", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ServeAppsFragment handles the HTMX request for the apps list.
func (h *Handler) ServeAppsFragment(w http.ResponseWriter, r *http.Request) {
	apps := h.Registry.List()

	// Sort apps by name for consistent display
	sort.Slice(apps, func(i, j int) bool {
		return apps[i].Name < apps[j].Name
	})

	viewModels := make([]AppView, len(apps))
	for i, app := range apps {
		viewModels[i] = toAppView(app)
	}

	data := AppsPageData{
		Apps: viewModels,
	}

	if err := h.Tmpl.ExecuteTemplate(w, "apps-list", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// HandleAddApp processes the form submission to add a new app.
func (h *Handler) HandleAddApp(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	form := AppFormData{
		Name:        strings.TrimSpace(r.FormValue("name")),
		RepoURL:     strings.TrimSpace(r.FormValue("repo_url")),
		RepoAuth:    strings.TrimSpace(r.FormValue("repo_auth_method")),
		DeployKey:   strings.TrimSpace(r.FormValue("deploy_key")),
		Branch:      strings.TrimSpace(r.FormValue("branch")),
		ComposePath: strings.TrimSpace(r.FormValue("compose_path")),
	}
	deployKey := form.DeployKey
	form.DeployKey = ""

	if form.RepoAuth == "" {
		form.RepoAuth = "public"
	}
	if form.Branch == "" {
		form.Branch = "main"
	}
	if form.ComposePath == "" {
		form.ComposePath = "compose.yaml"
	}

	if form.Name == "" || form.RepoURL == "" {
		h.renderNewAppPage(w, http.StatusBadRequest, form, "Name and repo URL are required.")
		return
	}

	app := &controller.App{
		Name:           form.Name,
		RepoURL:        form.RepoURL,
		RepoAuthMethod: form.RepoAuth,
		Branch:         form.Branch,
		ComposePath:    form.ComposePath,
	}

	if err := h.Registry.AddWithDeployKey(app, deployKey); err != nil {
		h.renderNewAppPage(w, http.StatusConflict, form, err.Error())
		return
	}

	if isHTMXRequest(r) {
		h.ServeAppsFragment(w, r)
		return
	}

	http.Redirect(w, r, "/ui/apps/"+app.ID, http.StatusSeeOther)
}

func (h *Handler) loadAppDetail(r *http.Request, id string) (AppDetailView, error) {
	app, err := h.Registry.Get(id)
	if err != nil {
		return AppDetailView{}, err
	}

	detail := toAppDetailView(app)

	// Fetch runtime container information if executor is available.
	if h.Executor != nil {
		projectName := compose.ProjectNameForApp(app.ID)
		containers, inspectErr := h.Executor.InspectProjectContainers(r.Context(), projectName)
		if inspectErr == nil {
			enrichWithContainerData(&detail, containers)
		}
	}

	return detail, nil
}

func (h *Handler) renderNewAppPage(w http.ResponseWriter, statusCode int, form AppFormData, errorMessage string) {
	w.WriteHeader(statusCode)
	data := AppsPageData{
		Page:  "new",
		Form:  form,
		Error: errorMessage,
	}
	if err := h.Tmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func isHTMXRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("HX-Request"), "true")
}

// enrichWithContainerData populates container-related fields on the detail view.
func enrichWithContainerData(detail *AppDetailView, containers []compose.ServiceContainer) {
	detail.ContainerCount = len(containers)
	unhealthyCount := 0
	for _, c := range containers {
		if c.Status == "running" {
			detail.RunningCount++
		}
		if c.Health == "unhealthy" {
			unhealthyCount++
		}
		detail.Services = append(detail.Services, ServiceView{
			Service: c.Service,
			Image:   c.Image,
			Status:  c.Status,
			Health:  c.Health,
			Ports:   c.Ports,
		})
	}
	detail.HealthLabel = healthLabel(detail.ContainerCount, detail.RunningCount, unhealthyCount)
}

func toAppView(app *controller.App) AppView {
	desired := strings.TrimSpace(app.LastSeenCommit)
	synced := strings.TrimSpace(app.LastSyncedCommit)
	inSync := desired != "" && synced != "" && desired == synced

	return AppView{
		ID:                  app.ID,
		Name:                app.Name,
		RepoURL:             app.RepoURL,
		RepoShort:           shortRepoURL(app.RepoURL),
		Branch:              app.Branch,
		Status:              app.Status,
		LastSyncAt:          formatTime(app.LastSyncAt),
		LastSyncAtRelative:  relativeTime(app.LastSyncAt),
		SyncedCommitShort:   shortHash(app.LastSyncedCommit),
		SyncedCommitMessage: fallbackString(app.LastSyncedCommitMessage, ""),
		InSync:              inSync,
	}
}

func toAppDetailView(app *controller.App) AppDetailView {
	desired := strings.TrimSpace(app.LastSeenCommit)
	synced := strings.TrimSpace(app.LastSyncedCommit)
	inSync := desired != "" && synced != "" && desired == synced

	return AppDetailView{
		ID:                      app.ID,
		Name:                    app.Name,
		RepoURL:                 app.RepoURL,
		RepoAuth:                fallbackString(app.RepoAuthMethod, "public"),
		Branch:                  app.Branch,
		ComposePath:             app.ComposePath,
		PollInterval:            app.PollInterval,
		LastSeenCommit:          fallbackString(app.LastSeenCommit, "n/a"),
		LastSeenCommitMessage:   fallbackString(app.LastSeenCommitMessage, "n/a"),
		LastSeenCommitShort:     shortHash(app.LastSeenCommit),
		LastSyncedCommit:        fallbackString(app.LastSyncedCommit, "n/a"),
		LastSyncedCommitMessage: fallbackString(app.LastSyncedCommitMessage, "n/a"),
		LastSyncedCommitShort:   shortHash(app.LastSyncedCommit),
		LastSyncOutput:          strings.TrimSpace(app.LastSyncOutput),
		LastSyncError:           strings.TrimSpace(app.LastSyncError),
		Status:                  app.Status,
		LastSyncAt:              formatTime(app.LastSyncAt),
		LastSyncAtRelative:      relativeTime(app.LastSyncAt),
		InSync:                  inSync,
		HealthLabel:             "No data",
	}
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "n/a"
	}
	return value.Format(time.RFC3339)
}

func relativeTime(value time.Time) string {
	if value.IsZero() {
		return "never"
	}
	d := time.Since(value)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	case d < 24*time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

func shortHash(hash string) string {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return "n/a"
	}
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}

func healthLabel(containerCount, runningCount, unhealthyCount int) string {
	if containerCount == 0 {
		return "No data"
	}
	if runningCount == containerCount && unhealthyCount == 0 {
		return "Healthy"
	}
	if runningCount == 0 {
		return "Down"
	}
	return "Degraded"
}

func shortRepoURL(repoURL string) string {
	u := strings.TrimSpace(repoURL)
	u = strings.TrimSuffix(u, ".git")

	// SSH format: git@github.com:org/repo
	if strings.HasPrefix(u, "git@") {
		if idx := strings.Index(u, ":"); idx > 0 && idx < len(u)-1 {
			return u[idx+1:]
		}
	}

	// HTTPS format: https://github.com/org/repo
	parts := strings.Split(u, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}

	return repoURL
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func formatCommit(hash, message string) string {
	hash = strings.TrimSpace(hash)
	message = strings.TrimSpace(message)
	if hash == "" {
		return "n/a"
	}

	short := hash
	if len(short) > 8 {
		short = short[:8]
	}
	if message == "" {
		return short
	}
	return short + " - " + message
}
