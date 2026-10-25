package handler

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
	"zero-reverse-proxy/internal/auth"
	"zero-reverse-proxy/internal/storage"
)

type APIHandler struct {
	storage    *storage.Storage
	httpClient *http.Client
}

func NewAPIHandler(store *storage.Storage) *APIHandler {
	return &APIHandler{
		storage: store,
		httpClient: &http.Client{
			Timeout: 8 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

type Response struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeSuccess(w http.ResponseWriter, data interface{}) {
	writeJSON(w, http.StatusOK, Response{Code: 0, Message: "ok", Data: data})
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, Response{Code: status, Message: msg})
}

// RequireAuth is the middleware ensuring valid admin credentials
func (h *APIHandler) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := h.storage.GetConfig()

		token := ""
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token = strings.TrimPrefix(authHeader, "Bearer ")
		}
		if token == "" {
			if c, err := r.Cookie("auth_token"); err == nil {
				token = c.Value
			}
		}

		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing authentication token")
			return
		}

		user, err := auth.ValidateToken(token, cfg.AuthSecret)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}

		if user != cfg.AdminUsername {
			writeError(w, http.StatusUnauthorized, "user mismatch")
			return
		}

		next(w, r)
	}
}

// RegisterRoutes registers all API routes on a mux
func (h *APIHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/login", h.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", h.handleLogout)
	mux.HandleFunc("GET /api/auth/me", h.RequireAuth(h.handleMe))
	mux.HandleFunc("POST /api/auth/password", h.RequireAuth(h.handleUpdatePassword))

	mux.HandleFunc("GET /api/rules", h.RequireAuth(h.handleListRules))
	mux.HandleFunc("POST /api/rules", h.RequireAuth(h.handleCreateRule))
	mux.HandleFunc("PUT /api/rules/{id}", h.RequireAuth(h.handleUpdateRule))
	mux.HandleFunc("PATCH /api/rules/{id}/toggle", h.RequireAuth(h.handleToggleRule))
	mux.HandleFunc("DELETE /api/rules/{id}", h.RequireAuth(h.handleDeleteRule))
	mux.HandleFunc("POST /api/rules/test", h.RequireAuth(h.handleTestConnection))
}

func (h *APIHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	cfg := h.storage.GetConfig()
	if req.Username != cfg.AdminUsername || !auth.CheckPassword(req.Password, cfg.AdminPassword) {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}

	// Valid for 7 days
	token, err := auth.GenerateToken(cfg.AdminUsername, cfg.AuthSecret, 7*24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "auth_token",
		Value:    token,
		Path:     "/",
		MaxAge:   7 * 86400,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	writeSuccess(w, map[string]interface{}{
		"token":    token,
		"username": cfg.AdminUsername,
	})
}

func (h *APIHandler) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "auth_token",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
	writeSuccess(w, "logged out")
}

func (h *APIHandler) handleMe(w http.ResponseWriter, r *http.Request) {
	cfg := h.storage.GetConfig()
	writeSuccess(w, map[string]interface{}{
		"username": cfg.AdminUsername,
		"port":     cfg.Port,
		"rule_num": len(cfg.Rules),
	})
}

func (h *APIHandler) handleUpdatePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.NewPassword) < 6 {
		writeError(w, http.StatusBadRequest, "new password must be at least 6 characters")
		return
	}

	cfg := h.storage.GetConfig()
	if !auth.CheckPassword(req.OldPassword, cfg.AdminPassword) {
		writeError(w, http.StatusBadRequest, "incorrect old password")
		return
	}

	if err := h.storage.UpdateAdminPassword(req.NewPassword); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save new password")
		return
	}

	writeSuccess(w, "password updated successfully")
}

func (h *APIHandler) handleListRules(w http.ResponseWriter, r *http.Request) {
	rules := h.storage.ListRules()
	writeSuccess(w, rules)
}

func (h *APIHandler) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	var rule storage.Rule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeError(w, http.StatusBadRequest, "invalid rule payload")
		return
	}

	created, err := h.storage.AddRule(rule)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeSuccess(w, created)
}

func (h *APIHandler) handleUpdateRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing rule id")
		return
	}

	var rule storage.Rule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeError(w, http.StatusBadRequest, "invalid rule payload")
		return
	}

	updated, err := h.storage.UpdateRule(id, rule)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeSuccess(w, updated)
}

func (h *APIHandler) handleToggleRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}

	rule, found := h.storage.GetRule(id)
	if !found {
		writeError(w, http.StatusNotFound, "rule not found")
		return
	}

	rule.Enabled = req.Enabled
	updated, err := h.storage.UpdateRule(id, rule)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeSuccess(w, updated)
}

func (h *APIHandler) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.storage.DeleteRule(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeSuccess(w, "deleted")
}

func (h *APIHandler) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TargetURL string `json:"target_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}

	targetURL := strings.TrimSpace(req.TargetURL)
	if _, err := url.ParseRequestURI(targetURL); err != nil {
		writeError(w, http.StatusBadRequest, "invalid target url")
		return
	}

	start := time.Now()
	resp, err := h.httpClient.Get(targetURL)
	latencyMs := time.Since(start).Milliseconds()

	if err != nil {
		writeSuccess(w, map[string]interface{}{
			"reachable":  false,
			"error":      err.Error(),
			"latency_ms": latencyMs,
		})
		return
	}
	_ = resp.Body.Close()

	writeSuccess(w, map[string]interface{}{
		"reachable":  true,
		"status_code": resp.StatusCode,
		"status":      resp.Status,
		"latency_ms":  latencyMs,
	})
}
