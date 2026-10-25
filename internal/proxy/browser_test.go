package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserMode_ParseURL(t *testing.T) {
	tests := []struct {
		path     string
		query    string
		rawURI   string
		wantHost string
		wantPath string
	}{
		{
			path:     "/b/https://example.com/test",
			rawURI:   "/b/https://example.com/test?a=1",
			wantHost: "example.com",
			wantPath: "/test",
		},
		{
			path:     "/b/http:/192.168.1.100:8080/api",
			rawURI:   "/b/http:/192.168.1.100:8080/api",
			wantHost: "192.168.1.100:8080",
			wantPath: "/api",
		},
		{
			path:     "/b/my-domain.com",
			rawURI:   "/b/my-domain.com",
			wantHost: "my-domain.com",
			wantPath: "/",
		},
	}

	for _, tt := range tests {
		target, subPath, err := parseBrowserURL(tt.path, tt.query, tt.rawURI)
		if err != nil {
			t.Errorf("unexpected error for %s: %v", tt.rawURI, err)
			continue
		}
		if target.Host != tt.wantHost {
			t.Errorf("expected host %s, got %s", tt.wantHost, target.Host)
		}
		if subPath != tt.wantPath {
			t.Errorf("expected subPath %s, got %s", tt.wantPath, subPath)
		}
	}
}

func TestBrowserMode_ProxyAndFallbacks(t *testing.T) {
	// Upstream mock server
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, "<!DOCTYPE html><html><head></head><body><h1>Browser Page</h1></body></html>")
		case "/redirect":
			http.Redirect(w, r, "/dashboard", http.StatusFound)
		case "/static/main.css":
			w.Header().Set("Content-Type", "text/css")
			fmt.Fprint(w, "body { color: red; }")
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	engine := NewEngine()

	// 1. Direct browser request to /b/{upstream.URL}/page
	browserPath := "/b/" + upstream.URL + "/page"
	req := httptest.NewRequest("GET", browserPath, nil)
	rec := httptest.NewRecorder()
	matched := engine.ServeHTTP(rec, req)
	if !matched || rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got matched=%v, code=%d", matched, rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	bodyStr := string(body)
	if !strings.Contains(bodyStr, "Browser Page") || !strings.Contains(bodyStr, "zrp-toolbar") {
		t.Errorf("expected body with content and toolbar: %s", bodyStr)
	}

	// 2. Redirect rewriting in browser mode
	req = httptest.NewRequest("GET", "/b/"+upstream.URL+"/redirect", nil)
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 Found, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	expectedLoc := "/b/" + upstream.URL + "/dashboard"
	if loc != expectedLoc {
		t.Errorf("expected Location %s, got %s", expectedLoc, loc)
	}

	// 3. Referer fallback for root-relative static asset
	fallbackReq := httptest.NewRequest("GET", "/static/main.css", nil)
	fallbackReq.Header.Set("Referer", "http://proxy.local/b/"+upstream.URL+"/page")
	rec = httptest.NewRecorder()
	matched = engine.ServeHTTP(rec, fallbackReq)
	if !matched || rec.Code != http.StatusOK {
		t.Fatalf("expected referer fallback to match, got matched=%v, code=%d", matched, rec.Code)
	}
	body, _ = io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "color: red") {
		t.Errorf("unexpected fallback body: %s", string(body))
	}
}
