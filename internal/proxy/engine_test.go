package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"zero-reverse-proxy/internal/storage"
)

func TestProxy_RoutingAndFeatures(t *testing.T) {
	// 1. Mock upstream server
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/hello":
			fmt.Fprintf(w, "hello from upstream, q=%s, host=%s", r.URL.Query().Get("q"), r.Host)
		case "/redirect":
			http.Redirect(w, r, "/login", http.StatusFound)
		case "/set-cookie":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "123", Path: "/"})
			fmt.Fprint(w, "cookie set")
		case "/page":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, "<!DOCTYPE html><html><head><title>Test</title></head><body><h1>Hello</h1></body></html>")
		case "/static/app.js":
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, "console.log('app.js');")
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	engine := NewEngine()
	rule := storage.Rule{
		ID:          "rule-1",
		Name:        "Test Upstream",
		Slug:        "app",
		TargetURL:   upstream.URL,
		StripPrefix: true,
		InjectBase:  true,
		Enabled:     true,
	}
	engine.UpdateRules([]storage.Rule{rule})

	// Test 1: Direct request forwarding and prefix stripping
	req := httptest.NewRequest("GET", "http://proxy.local/p/app/api/hello?q=world", nil)
	rec := httptest.NewRecorder()
	matched := engine.ServeHTTP(rec, req)
	if !matched || rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got matched=%v, code=%d", matched, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "hello from upstream, q=world") {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}

	// Test 2: Redirect Location rewriting (relative and upstream absolute URL)
	req = httptest.NewRequest("GET", "http://proxy.local/p/app/redirect", nil)
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 Found, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/p/app/login" {
		t.Errorf("expected rewritten location /p/app/login, got %s", loc)
	}

	// Test 2.1: Absolute URL redirect pointing to upstream target itself
	absRedirectLoc := rewriteLocation(upstream.URL+"/", rule)
	if absRedirectLoc != "/p/app/" {
		t.Errorf("expected rewritten location /p/app/, got %s", absRedirectLoc)
	}

	// Test 3: Cookie Path rewriting
	req = httptest.NewRequest("GET", "http://proxy.local/p/app/set-cookie", nil)
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	cookie := rec.Header().Get("Set-Cookie")
	if !strings.Contains(cookie, "Path=/") {
		t.Errorf("expected Cookie path to be /, got %s", cookie)
	}

	// Test 4: HTML <base> injection
	req = httptest.NewRequest("GET", "http://proxy.local/p/app/page", nil)
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `<base href="/p/app/">`) {
		t.Errorf("expected <base> tag injected, got: %s", rec.Body.String())
	}

	// Test 5: Referer Fallback for root-relative static assets
	fallbackReq := httptest.NewRequest("GET", "http://proxy.local/static/app.js", nil)
	fallbackReq.Header.Set("Referer", "http://proxy.local/p/app/page")
	rec = httptest.NewRecorder()
	matched = engine.ServeHTTP(rec, fallbackReq)
	if !matched || rec.Code != http.StatusOK {
		t.Fatalf("expected fallback to match and return 200, got matched=%v, code=%d", matched, rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "app.js") {
		t.Errorf("unexpected fallback response: %s", string(body))
	}

	// Test 6: Fallback for root-relative API request (e.g. /api/hello), ensuring it is NOT intercepted by proxy
	apiFallbackReq := httptest.NewRequest("GET", "http://proxy.local/api/hello?q=fallback", nil)
	apiFallbackReq.Header.Set("Referer", "http://proxy.local/p/app/page")
	rec = httptest.NewRecorder()
	matched = engine.ServeHTTP(rec, apiFallbackReq)
	if !matched || rec.Code != http.StatusOK {
		t.Fatalf("expected api fallback to match upstream, got matched=%v, code=%d", matched, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "hello from upstream, q=fallback") {
		t.Errorf("unexpected api fallback body: %s", rec.Body.String())
	}

	// Test 7: Cookie-based fallback for requests without referer
	cookieFallbackReq := httptest.NewRequest("GET", "http://proxy.local/api/hello?q=cookie", nil)
	cookieFallbackReq.AddCookie(&http.Cookie{Name: RuleCookieSlug, Value: "app"})
	rec = httptest.NewRecorder()
	matched = engine.ServeHTTP(rec, cookieFallbackReq)
	if !matched || rec.Code != http.StatusOK {
		t.Fatalf("expected cookie fallback to match upstream, got matched=%v, code=%d", matched, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "hello from upstream, q=cookie") {
		t.Errorf("unexpected cookie fallback body: %s", rec.Body.String())
	}
}
