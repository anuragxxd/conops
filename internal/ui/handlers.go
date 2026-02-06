package ui

import (
	"html/template"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/conops/conops/internal/controller"
	"github.com/go-chi/chi/v5"
)

// Handler manages UI requests.
type Handler struct {
	Registry *controller.Registry
	Tmpl     *template.Template
}

// AppView is the view model for an app in the list.
type AppView struct {
	ID            string
	Name          string
	RepoURL       string
	RepoAuth      string
	Branch        string
	Status        string
	LastSyncAt    string
	DesiredCommit string
	SyncedCommit  string
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
	LastSyncedCommit        string
	LastSyncedCommitMessage string
	LastSyncOutput          string
	LastSyncError           string
	Status                  string
	LastSyncAt              string
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
func NewHandler(registry *controller.Registry, templateDir string) (*Handler, error) {
	tmpl, err := template.ParseGlob(filepath.Join(templateDir, "*.html"))
	if err != nil {
		return nil, err
	}

	return &Handler{
		Registry: registry,
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
	app, err := h.Registry.Get(id)
	if err != nil {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	data := AppsPageData{
		Page: "detail",
		App:  toAppDetailView(app),
	}
	if err := h.Tmpl.ExecuteTemplate(w, "base", data); err != nil {
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

func toAppView(app *controller.App) AppView {
	return AppView{
		ID:            app.ID,
		Name:          app.Name,
		RepoURL:       app.RepoURL,
		RepoAuth:      fallbackString(app.RepoAuthMethod, "public"),
		Branch:        app.Branch,
		Status:        app.Status,
		LastSyncAt:    formatTime(app.LastSyncAt),
		DesiredCommit: formatCommit(app.LastSeenCommit, app.LastSeenCommitMessage),
		SyncedCommit:  formatCommit(app.LastSyncedCommit, app.LastSyncedCommitMessage),
	}
}

func toAppDetailView(app *controller.App) AppDetailView {
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
		LastSyncedCommit:        fallbackString(app.LastSyncedCommit, "n/a"),
		LastSyncedCommitMessage: fallbackString(app.LastSyncedCommitMessage, "n/a"),
		LastSyncOutput:          strings.TrimSpace(app.LastSyncOutput),
		LastSyncError:           strings.TrimSpace(app.LastSyncError),
		Status:                  app.Status,
		LastSyncAt:              formatTime(app.LastSyncAt),
	}
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "n/a"
	}
	return value.Format(time.RFC3339)
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
