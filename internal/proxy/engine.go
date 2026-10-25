package proxy

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"zero-reverse-proxy/internal/storage"
)

// Engine handles dynamic routing and reverse proxying
type Engine struct {
	mu        sync.RWMutex
	rules     map[string]*ruleProxy
	transport *http.Transport
}

type ruleProxy struct {
	rule      storage.Rule
	targetURL *url.URL
	proxy     *httputil.ReverseProxy
}

// NewEngine creates a new proxy engine
func NewEngine() *Engine {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          500,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // Allow proxying to self-signed internal endpoints
		},
	}

	return &Engine{
		rules:     make(map[string]*ruleProxy),
		transport: transport,
	}
}

// UpdateRules atomically replaces the routing rules in memory
func (e *Engine) UpdateRules(rules []storage.Rule) {
	e.mu.Lock()
	defer e.mu.Unlock()

	newMap := make(map[string]*ruleProxy, len(rules))
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		target, err := url.Parse(r.TargetURL)
		if err != nil {
			log.Printf("[Proxy] Invalid target URL %s for rule %s: %v", r.TargetURL, r.Slug, err)
			continue
		}
		rp := &ruleProxy{
			rule:      r,
			targetURL: target,
		}
		rp.proxy = e.createReverseProxy(rp)
		newMap[strings.ToLower(r.Slug)] = rp
	}
	e.rules = newMap
}

func (e *Engine) createReverseProxy(rp *ruleProxy) *httputil.ReverseProxy {
	rule := rp.rule
	target := rp.targetURL

	p := &httputil.ReverseProxy{
		Transport: e.transport,
		Director: func(req *http.Request) {
			// Determine subPath
			subPath := req.URL.Path
			prefix := "/p/" + rule.Slug
			if strings.HasPrefix(strings.ToLower(subPath), strings.ToLower(prefix)) {
				if rule.StripPrefix {
					subPath = subPath[len(prefix):]
					if subPath == "" {
						subPath = "/"
					}
				}
			}

			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = singleJoiningSlash(target.Path, subPath)

			if target.RawQuery == "" || req.URL.RawQuery == "" {
				req.URL.RawQuery = target.RawQuery + req.URL.RawQuery
			} else {
				req.URL.RawQuery = target.RawQuery + "&" + req.URL.RawQuery
			}

			if rule.PassHost {
				// keep original Host
			} else {
				req.Host = target.Host
			}

			// Set standard proxy headers
			if clientIP, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
				if prior, ok := req.Header["X-Forwarded-For"]; ok {
					clientIP = strings.Join(prior, ", ") + ", " + clientIP
				}
				req.Header.Set("X-Forwarded-For", clientIP)
				req.Header.Set("X-Real-IP", clientIP)
			}
			req.Header.Set("X-Forwarded-Proto", reqScheme(req))
			req.Header.Set("X-Forwarded-Host", req.Host)

			// Apply custom headers
			for k, v := range rule.Headers {
				req.Header.Set(k, v)
			}

			// For web HTML base injection to work, avoid compression for HTML candidate requests
			if rule.InjectBase && strings.Contains(req.Header.Get("Accept"), "text/html") {
				req.Header.Del("Accept-Encoding")
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			// 1. Rewrite Location header for redirects
			if loc := resp.Header.Get("Location"); loc != "" {
				resp.Header.Set("Location", rewriteLocation(loc, rule, resp.Request))
			}

			// 2. Rewrite Cookie Path and Domain
			if cookies := resp.Header["Set-Cookie"]; len(cookies) > 0 {
				newCookies := make([]string, 0, len(cookies))
				for _, cookie := range cookies {
					newCookies = append(newCookies, rewriteCookie(cookie, rule))
				}
				resp.Header["Set-Cookie"] = newCookies
			}

			// 3. Inject <base href="/p/{slug}/"> for HTML documents
			if rule.InjectBase && strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
				bodyBytes, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil {
					return err
				}

				injected := injectBaseHref(bodyBytes, "/p/"+rule.Slug+"/")
				resp.Body = io.NopCloser(bytes.NewReader(injected))
				resp.ContentLength = int64(len(injected))
				resp.Header.Set("Content-Length", strconv.Itoa(len(injected)))
			}

			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			log.Printf("[Proxy Error] %s -> %s: %v", req.URL.Path, rule.TargetURL, err)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, "<h3>502 Bad Gateway</h3><p>Failed to proxy request to target service (%s): %v</p>", rule.TargetURL, err)
		},
	}
	return p
}

// Match matches a request to a rule proxy
func (e *Engine) Match(r *http.Request) (*ruleProxy, string, bool) {
	path := r.URL.Path

	// 1. Direct path match: /p/{slug} or /p/{slug}/*
	if strings.HasPrefix(path, "/p/") || path == "/p" {
		trimmed := strings.TrimPrefix(path, "/p/")
		if trimmed == "" {
			return nil, "", false
		}
		parts := strings.SplitN(trimmed, "/", 2)
		slug := parts[0]

		e.mu.RLock()
		rp, ok := e.rules[strings.ToLower(slug)]
		e.mu.RUnlock()
		if ok {
			return rp, slug, true
		}
		return nil, slug, false
	}

	// 2. Referer fallback match: when a web page loads root-relative assets (e.g. /static/bundle.js)
	if !strings.HasPrefix(path, "/admin") && !strings.HasPrefix(path, "/api") {
		referer := r.Header.Get("Referer")
		if referer != "" {
			if refURL, err := url.Parse(referer); err == nil {
				if strings.HasPrefix(refURL.Path, "/p/") {
					parts := strings.SplitN(strings.TrimPrefix(refURL.Path, "/p/"), "/", 2)
					slug := parts[0]
					e.mu.RLock()
					rp, ok := e.rules[strings.ToLower(slug)]
					e.mu.RUnlock()
					if ok {
						return rp, slug, true
					}
				}
			}
		}
	}

	return nil, "", false
}

// ServeHTTP dispatches matching proxy requests
func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) bool {
	rp, slug, matched := e.Match(r)
	if !matched {
		if slug != "" && strings.HasPrefix(r.URL.Path, "/p/") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, "<h3>404 Rule Not Found</h3><p>Proxy rule for slug <code>%s</code> is not configured or disabled.</p>", slug)
			return true
		}
		return false
	}

	// Ensure /p/{slug} ends with slash for proper relative path resolution in browsers
	if r.URL.Path == "/p/"+slug {
		target := "/p/" + slug + "/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
		return true
	}

	rp.proxy.ServeHTTP(w, r)
	return true
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		if a == "" || b == "" {
			return a + b
		}
		return a + "/" + b
	}
	return a + b
}

func reqScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		return proto
	}
	return "http"
}

func rewriteLocation(loc string, rule storage.Rule, req *http.Request) string {
	prefix := "/p/" + rule.Slug
	u, err := url.Parse(loc)
	if err != nil {
		return loc
	}

	// If relative redirect (e.g. /login or login)
	if u.Host == "" {
		if strings.HasPrefix(u.Path, "/") {
			u.Path = prefix + u.Path
		} else {
			u.Path = prefix + "/" + u.Path
		}
		return u.String()
	}

	// If redirect points to the upstream host itself, rewrite host and add prefix
	targetURL, _ := url.Parse(rule.TargetURL)
	if targetURL != nil && strings.EqualFold(u.Host, targetURL.Host) {
		u.Scheme = reqScheme(req)
		u.Host = req.Host
		u.Path = prefix + u.Path
		return u.String()
	}

	return loc
}

func rewriteCookie(rawCookie string, rule storage.Rule) string {
	parts := strings.Split(rawCookie, ";")
	var newParts []string
	hasPath := false

	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "domain=") {
			// Drop upstream domain restrictions so client accepts cookie
			continue
		}
		if strings.HasPrefix(lower, "path=") {
			hasPath = true
			newParts = append(newParts, "Path=/p/"+rule.Slug)
			continue
		}
		newParts = append(newParts, trimmed)
	}
	if !hasPath {
		newParts = append(newParts, "Path=/p/"+rule.Slug)
	}
	return strings.Join(newParts, "; ")
}

func injectBaseHref(htmlBytes []byte, baseHref string) []byte {
	// Check if <base tag already exists
	lowerHTML := bytes.ToLower(htmlBytes)
	if bytes.Contains(lowerHTML, []byte("<base ")) || bytes.Contains(lowerHTML, []byte("<base>")) {
		return htmlBytes
	}

	tag := []byte("<base href=\"" + baseHref + "\">\n")

	// Insert right after <head> or <head ...>
	idx := bytes.Index(lowerHTML, []byte("<head"))
	if idx != -1 {
		closeIdx := bytes.IndexByte(lowerHTML[idx:], '>')
		if closeIdx != -1 {
			insertPos := idx + closeIdx + 1
			var res bytes.Buffer
			res.Grow(len(htmlBytes) + len(tag))
			res.Write(htmlBytes[:insertPos])
			res.Write(tag)
			res.Write(htmlBytes[insertPos:])
			return res.Bytes()
		}
	}

	// Fallback: prepend to start
	return append(tag, htmlBytes...)
}
