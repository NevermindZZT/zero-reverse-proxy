package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"zero-reverse-proxy/internal/handler"
	"zero-reverse-proxy/internal/proxy"
	"zero-reverse-proxy/internal/storage"
	"zero-reverse-proxy/web"
)

func TestServer_Integration(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := storage.NewStorage(filepath.Join(tmpDir, "config.json"))
	if err != nil {
		t.Fatalf("failed to init storage: %v", err)
	}

	engine := proxy.NewEngine()
	engine.UpdateRules(store.ListRules())
	store.OnChange(func(rules []storage.Rule) {
		engine.UpdateRules(rules)
	})

	mux := http.NewServeMux()
	apiHandler := handler.NewAPIHandler(store)
	apiHandler.RegisterRoutes(mux)

	webHandler := http.StripPrefix("/admin", web.Handler())
	mux.Handle("/admin/", webHandler)
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})

	masterHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if engine.ServeHTTP(w, r) {
			return
		}
		mux.ServeHTTP(w, r)
	})

	ts := httptest.NewServer(masterHandler)
	defer ts.Close()

	client := ts.Client()

	// 1. Test root redirect to /admin/
	// Do not follow redirect to check status code
	req, _ := http.NewRequest("GET", ts.URL+"/", nil)
	resp, err := client.Transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("failed to request root: %v", err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Errorf("expected 302 Found from root, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// 2. Test admin UI returns embedded index.html
	resp, err = client.Get(ts.URL + "/admin/")
	if err != nil {
		t.Fatalf("failed to request /admin/: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK from /admin/, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), "Zero Reverse Proxy") {
		t.Errorf("expected HTML content to contain Zero Reverse Proxy, got: %s", string(body))
	}
}
