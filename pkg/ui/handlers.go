package ui

import (
	"html/template"
	"net/http"
	"path/filepath"
	"sort"
	"time"

	"github.com/conops/conops/pkg/controller"
)

// Handler manages UI requests.
type Handler struct {
	Registry *controller.Registry
	Tmpl     *template.Template
}

// AppView is the view model for an app in the list.
type AppView struct {
	Name           string
	RepoURL        string
	Branch         string
	Status         string
	LastSyncAt     string
}

// AppsPageData is the data passed to the apps page template.
type AppsPageData struct {
	Apps []AppView
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

// ServeHTTP handles the main UI page request.
func (h *Handler) ServeAppsPage(w http.ResponseWriter, r *http.Request) {
	// For the full page, we don't need to load apps yet if the apps-list fragment does it.
	// But let's pass an empty struct or minimal data if needed.
	if err := h.Tmpl.ExecuteTemplate(w, "base", nil); err != nil {
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
		viewModels[i] = AppView{
			Name:       app.Name,
			RepoURL:    app.RepoURL,
			Branch:     app.Branch,
			Status:     app.Status,
			LastSyncAt: app.LastSyncAt.Format(time.RFC3339),
		}
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

	app := &controller.App{
		ID:          r.FormValue("id"),
		Name:        r.FormValue("name"),
		RepoURL:     r.FormValue("repo_url"),
		Branch:      r.FormValue("branch"),
		ComposePath: r.FormValue("compose_path"),
	}

	if app.ID == "" || app.Name == "" || app.RepoURL == "" {
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	if err := h.Registry.Add(app); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	// After adding, return the updated list fragment
	h.ServeAppsFragment(w, r)
}
