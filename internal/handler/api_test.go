package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"zero-reverse-proxy/internal/storage"
)

func TestAPIHandler_Flow(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := storage.NewStorage(filepath.Join(tmpDir, "config.json"))
	if err != nil {
		t.Fatalf("failed to init storage: %v", err)
	}

	handler := NewAPIHandler(store)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	// 1. Unauthorized access to /api/rules
	req := httptest.NewRequest("GET", "/api/rules", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rec.Code)
	}

	// 2. Login with correct credentials
	loginBody := []byte(`{"username":"admin","password":"admin123"}`)
	req = httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(loginBody))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on login, got %d, body: %s", rec.Code, rec.Body.String())
	}

	var loginResp struct {
		Code int `json:"code"`
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("failed to parse login resp: %v", err)
	}
	token := loginResp.Data.Token
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	// 3. Create rule with token
	ruleBody := []byte(`{"name":"My Service","slug":"mysvc","target_url":"http://127.0.0.1:8080","strip_prefix":true,"inject_base":true,"enabled":true}`)
	req = httptest.NewRequest("POST", "/api/rules", bytes.NewReader(ruleBody))
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK creating rule, got %d: %s", rec.Code, rec.Body.String())
	}

	// 4. List rules
	req = httptest.NewRequest("GET", "/api/rules", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK listing rules, got %d", rec.Code)
	}

	var listResp struct {
		Data []storage.Rule `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &listResp)
	if len(listResp.Data) != 1 || listResp.Data[0].Slug != "mysvc" {
		t.Fatalf("unexpected rules list: %+v", listResp.Data)
	}
	ruleID := listResp.Data[0].ID

	// 5. Delete rule
	req = httptest.NewRequest("DELETE", "/api/rules/"+ruleID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK deleting rule, got %d", rec.Code)
	}
}
