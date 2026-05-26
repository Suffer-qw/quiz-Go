package app

import (
	"context"
	"embed"
	"encoding/json"
	"html/template"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"quiz-go/internal/db"
)

type Config struct {
	DBPath        string
	SessionMaxAge time.Duration
}

type App struct {
	cfg       Config
	store     *db.Store
	templates *template.Template
}

//go:embed templates/*.html templates/pages/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

type ctxKey string

const ctxUserKey ctxKey = "user"

var mimeTypes = map[string]string{
	".css":  "text/css; charset=utf-8",
	".js":   "application/javascript; charset=utf-8",
	".html": "text/html; charset=utf-8",
	".svg":  "image/svg+xml",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".ico":  "image/x-icon",
	".woff": "font/woff",
	".woff2": "font/woff2",
}

func New(cfg Config) (*App, error) {
	store, err := db.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(); err != nil {
		return nil, err
	}
	if err := store.Seed(); err != nil {
		return nil, err
	}

	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}

	return &App{
		cfg:       cfg,
		store:     store,
		templates: tmpl,
	}, nil
}

func parseTemplates() (*template.Template, error) {
	t := template.New("root").Funcs(template.FuncMap{
		"safe": func(s string) template.HTML { return template.HTML(s) },
	})

	entries, err := templatesFS.ReadDir("templates")
	if err != nil {
		return nil, err
	}
	_ = entries

	paths := []string{
		"templates/layout.html",
		"templates/pages/index.html",
		"templates/pages/login.html",
		"templates/pages/register.html",
		"templates/pages/tests.html",
		"templates/pages/test_take.html",
		"templates/pages/history.html",
		"templates/pages/admin_tests.html",
		"templates/pages/admin_test_new.html",
	}
	for i, p := range paths {
		b, err := templatesFS.ReadFile(p)
		if err != nil {
			return nil, err
		}
		name := filepath.ToSlash(strings.TrimPrefix(p, "templates/"))
		if i == 0 {
			t, err = t.Parse(string(b))
			if err != nil {
				return nil, err
			}
			continue
		}
		_, err = t.New(name).Parse(string(b))
		if err != nil {
			return nil, err
		}
	}
	return t, nil
}


func (a *App) handleStatic(w http.ResponseWriter, r *http.Request) {
    fsPath := strings.TrimPrefix(r.URL.Path, "/")
    log.Printf("DEBUG static: path=%q fsPath=%q", r.URL.Path, fsPath)
    data, err := staticFS.ReadFile(fsPath)
    if err != nil {
        log.Printf("DEBUG static: ReadFile error: %v", err)
        http.NotFound(w, r)
        return
    }
    ext := filepath.Ext(fsPath)
    if ct, ok := mimeTypes[ext]; ok {
        w.Header().Set("Content-Type", ct)
    }
    w.WriteHeader(http.StatusOK)
    _, _ = w.Write(data)
}
/*

func (a *App) handleStatic(w http.ResponseWriter, r *http.Request) {
	// strip leading slash to get the FS-relative path: "static/style.css"
	fsPath := strings.TrimPrefix(r.URL.Path, "/")
	data, err := staticFS.ReadFile(fsPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ext := filepath.Ext(fsPath)
	if ct, ok := mimeTypes[ext]; ok {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
*/
func (a *App) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /static/", a.handleStatic)
	mux.HandleFunc("HEAD /static/", a.handleStatic)

	mux.HandleFunc("GET /", a.handleIndex)
	mux.HandleFunc("GET /login", a.handleLoginGet)
	mux.HandleFunc("POST /login", a.handleLoginPost)
	mux.HandleFunc("GET /register", a.handleRegisterGet)
	mux.HandleFunc("POST /register", a.handleRegisterPost)
	mux.HandleFunc("POST /logout", a.handleLogoutPost)

	mux.HandleFunc("GET /tests", a.requireAuth(a.handleTests))
	mux.HandleFunc("GET /tests/{id}", a.requireAuth(a.handleTestTakeGet))
	mux.HandleFunc("POST /tests/{id}/submit", a.requireAuth(a.handleTestSubmit))
	mux.HandleFunc("GET /history", a.requireAuth(a.handleHistory))

	mux.HandleFunc("GET /admin/tests", a.requireAdmin(a.handleAdminTests))
	mux.HandleFunc("GET /admin/tests/new", a.requireAdmin(a.handleAdminTestNewGet))
	mux.HandleFunc("POST /admin/tests/new", a.requireAdmin(a.handleAdminTestNewPost))
}

func (a *App) WithMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := a.store.GetUserByRequest(r)
		if u != nil {
			r = r.WithContext(context.WithValue(r.Context(), ctxUserKey, u))
		}
		next.ServeHTTP(w, r)
	})
}

func currentUser(r *http.Request) *db.User {
	if v := r.Context().Value(ctxUserKey); v != nil {
		if u, ok := v.(*db.User); ok {
			return u
		}
	}
	return nil
}

func (a *App) requireAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if currentUser(r) == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		h(w, r)
	}
}

func (a *App) requireAdmin(h http.HandlerFunc) http.HandlerFunc {
	return a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		if u == nil || u.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h(w, r)
	})
}

func (a *App) render(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["User"] = currentUser(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.templates.ExecuteTemplate(w, name, data)
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "pages/index.html", nil)
}

func (a *App) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "pages/login.html", nil)
}

func (a *App) handleRegisterGet(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "pages/register.html", nil)
}

func (a *App) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	u, err := a.store.Authenticate(username, password)
	if err != nil {
		a.render(w, r, "pages/login.html", map[string]any{"Error": "Неверный логин или пароль"})
		return
	}
	if err := a.store.CreateSession(w, u.ID, a.cfg.SessionMaxAge); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/tests", http.StatusSeeOther)
}

func (a *App) handleRegisterPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	if len(username) < 3 || len(password) < 6 {
		a.render(w, r, "pages/register.html", map[string]any{"Error": "Минимум: логин 3+, пароль 6+"})
		return
	}
	u, err := a.store.CreateUser(username, password, "user")
	if err != nil {
		a.render(w, r, "pages/register.html", map[string]any{"Error": "Такой пользователь уже существует"})
		return
	}
	if err := a.store.CreateSession(w, u.ID, a.cfg.SessionMaxAge); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/tests", http.StatusSeeOther)
}

func (a *App) handleLogoutPost(w http.ResponseWriter, r *http.Request) {
	_ = a.store.DeleteSession(w, r)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) handleTests(w http.ResponseWriter, r *http.Request) {
	tests, err := a.store.ListTests()
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	a.render(w, r, "pages/tests.html", map[string]any{"Tests": tests})
}

func (a *App) handleTestTakeGet(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	t, qs, err := a.store.GetTestWithQuestions(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a.render(w, r, "pages/test_take.html", map[string]any{
		"Test":      t,
		"Questions": qs,
	})
}

type submitPayload struct {
	Answers map[string]int `json:"answers"`
}

func (a *App) handleTestSubmit(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var p submitPayload
	if err := json.Unmarshal(body, &p); err != nil || len(p.Answers) == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	res, err := a.store.GradeAndStoreAttempt(u.ID, id, p.Answers)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(res)
}

func (a *App) handleHistory(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	items, err := a.store.ListRecentAttempts(u.ID, 10)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	a.render(w, r, "pages/history.html", map[string]any{"Items": items})
}

func (a *App) handleAdminTests(w http.ResponseWriter, r *http.Request) {
	tests, err := a.store.ListTests()
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	a.render(w, r, "pages/admin_tests.html", map[string]any{"Tests": tests})
}

func (a *App) handleAdminTestNewGet(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "pages/admin_test_new.html", nil)
}

type adminNewTestPayload struct {
	Title       string             `json:"title"`
	Description string             `json:"description"`
	Questions   []adminNewQuestion `json:"questions"`
}
type adminNewQuestion struct {
	Text         string   `json:"text"`
	Options      []string `json:"options"`
	CorrectIndex int      `json:"correctIndex"`
}

func (a *App) handleAdminTestNewPost(w http.ResponseWriter, r *http.Request) {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var p adminNewTestPayload
	if err := json.Unmarshal(b, &p); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	p.Title = strings.TrimSpace(p.Title)
	if p.Title == "" || len(p.Questions) == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	for _, q := range p.Questions {
		if strings.TrimSpace(q.Text) == "" || len(q.Options) != 4 || q.CorrectIndex < 0 || q.CorrectIndex > 3 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
	}
	qs := make([]db.AdminQuestion, 0, len(p.Questions))
	for _, q := range p.Questions {
		qs = append(qs, db.AdminQuestion{
			Text:         q.Text,
			Options:      q.Options,
			CorrectIndex: q.CorrectIndex,
		})
	}
	testID, err := a.store.CreateTestFromAdmin(p.Title, p.Description, qs)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": testID})
}