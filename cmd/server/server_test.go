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
	mux.HandleFunc("GET /favicon.ico", web.FaviconIcoHandler)
	mux.HandleFunc("GET /favicon.svg", web.FaviconSvgHandler)

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

	// 3. Test /favicon.ico returns 200 and image/x-icon
	resp, err = client.Get(ts.URL + "/favicon.ico")
	if err != nil {
		t.Fatalf("failed to request /favicon.ico: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK from /favicon.ico, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/x-icon" {
		t.Errorf("expected Content-Type image/x-icon, got %s", ct)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if len(body) == 0 {
		t.Errorf("expected non-empty favicon.ico content")
	}

	// 4. Test /favicon.svg returns 200 and image/svg+xml
	resp, err = client.Get(ts.URL + "/favicon.svg")
	if err != nil {
		t.Fatalf("failed to request /favicon.svg: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK from /favicon.svg, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("expected Content-Type image/svg+xml, got %s", ct)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), "<svg") {
		t.Errorf("expected SVG content from /favicon.svg")
	}

	// 5. Test /favicon.ico with Cookie fallback should still return Zero Proxy favicon
	icoReq, _ := http.NewRequest("GET", ts.URL+"/favicon.ico", nil)
	icoReq.AddCookie(&http.Cookie{Name: proxy.RuleCookieSlug, Value: "some-rule"})
	resp, err = client.Do(icoReq)
	if err != nil {
		t.Fatalf("failed to request /favicon.ico with cookie: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK from /favicon.ico with cookie, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
}
