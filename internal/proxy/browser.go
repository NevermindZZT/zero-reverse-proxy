package proxy

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

const (
	BrowserCookieTarget = "zrp_browser_target"
)

// parseBrowserURL extracts the target base URL and remaining subpath from a /b/ request
func parseBrowserURL(reqPath, rawQuery, rawURI string) (*url.URL, string, error) {
	// Candidate 1: Try parse directly from rawURI to preserve unescaped schemes
	uriStr := rawURI
	if idx := strings.Index(uriStr, "/b/"); idx != -1 {
		uriStr = uriStr[idx+3:]
	} else if strings.HasPrefix(uriStr, "/b") {
		uriStr = strings.TrimPrefix(uriStr, "/b")
	}

	// Remove leading slashes
	uriStr = strings.TrimLeft(uriStr, "/")
	if uriStr == "" {
		return nil, "", fmt.Errorf("empty browser target url")
	}

	// Normalize scheme if collapsed by http path cleaning (e.g. http:/ or https:/)
	if strings.HasPrefix(uriStr, "https:/") && !strings.HasPrefix(uriStr, "https://") {
		uriStr = "https://" + strings.TrimPrefix(uriStr, "https:/")
	} else if strings.HasPrefix(uriStr, "http:/") && !strings.HasPrefix(uriStr, "http://") {
		uriStr = "http://" + strings.TrimPrefix(uriStr, "http:/")
	} else if !strings.HasPrefix(uriStr, "http://") && !strings.HasPrefix(uriStr, "https://") {
		// Default to http:// if no scheme is provided
		uriStr = "http://" + uriStr
	}

	// Parse target URL
	target, err := url.Parse(uriStr)
	if err != nil || target.Host == "" {
		return nil, "", fmt.Errorf("invalid browser target: %v", err)
	}

	subPath := target.Path
	if subPath == "" {
		subPath = "/"
	}
	return target, subPath, nil
}

// serveBrowser handles dynamic ad-hoc reverse proxying for arbitrary URLs
func (e *Engine) serveBrowser(w http.ResponseWriter, r *http.Request, target *url.URL, subPath string) {
	targetOrigin := fmt.Sprintf("%s://%s", target.Scheme, target.Host)

	proxy := &httputil.ReverseProxy{
		Transport: e.transport,
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = subPath
			req.URL.RawQuery = target.RawQuery
			if r.URL.RawQuery != "" && target.RawQuery != r.URL.RawQuery {
				if req.URL.RawQuery == "" {
					req.URL.RawQuery = r.URL.RawQuery
				} else {
					req.URL.RawQuery += "&" + r.URL.RawQuery
				}
			}
			req.Host = target.Host

			// Standard proxy headers
			if clientIP, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
				req.Header.Set("X-Forwarded-For", clientIP)
				req.Header.Set("X-Real-IP", clientIP)
			}
			req.Header.Set("X-Forwarded-Proto", reqScheme(req))
			req.Header.Set("X-Forwarded-Host", req.Host)

			// For HTML modification, avoid compression
			if strings.Contains(req.Header.Get("Accept"), "text/html") {
				req.Header.Del("Accept-Encoding")
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			// 1. Remember last browser target in a session cookie for root fallback
			http.SetCookie(w, &http.Cookie{
				Name:     BrowserCookieTarget,
				Value:    targetOrigin,
				Path:     "/",
				SameSite: http.SameSiteLaxMode,
			})

			// 2. Strip frame-blocking headers to maximize web compatibility
			resp.Header.Del("X-Frame-Options")
			resp.Header.Del("Content-Security-Policy")

			// 3. Rewrite redirect Location headers
			if loc := resp.Header.Get("Location"); loc != "" {
				resp.Header.Set("Location", rewriteBrowserLocation(loc, targetOrigin))
			}

			// 4. Relax Set-Cookie headers so the client accepts them
			if cookies := resp.Header["Set-Cookie"]; len(cookies) > 0 {
				newCookies := make([]string, 0, len(cookies))
				for _, c := range cookies {
					newCookies = append(newCookies, sanitizeBrowserCookie(c))
				}
				resp.Header["Set-Cookie"] = newCookies
			}

			// 5. Inject <base> and lightweight top proxy bar for HTML documents
			if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
				bodyBytes, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil {
					return err
				}

				baseHref := fmt.Sprintf("/b/%s/", targetOrigin)
				injected := injectBaseHref(bodyBytes, baseHref)
				injected = injectBrowserToolbarAndInterceptors(injected, targetOrigin)
				resp.Body = io.NopCloser(bytes.NewReader(injected))
				resp.ContentLength = int64(len(injected))
				resp.Header.Set("Content-Length", strconv.Itoa(len(injected)))
			}

			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			log.Printf("[Browser Mode Error] %s -> %s: %v", req.URL.Path, target.String(), err)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, "<div style=\"font-family:sans-serif;padding:24px;\"><h3>502 Bad Gateway</h3><p>无法代理访问目标地址: <code>%s</code></p><p style=\"color:#666;font-size:12px;\">错误信息: %v</p><p><a href=\"/admin/\">返回控制台</a></p></div>", target.String(), err)
		},
	}

	proxy.ServeHTTP(w, r)
}

func rewriteBrowserLocation(loc, targetOrigin string) string {
	u, err := url.Parse(loc)
	if err != nil {
		return loc
	}
	originURL, _ := url.Parse(targetOrigin)

	// Internal relative redirect: e.g. /login -> /b/https://example.com/login
	if u.Host == "" {
		path := u.Path
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		return fmt.Sprintf("/b/%s%s%s", targetOrigin, path, queryAndFragment(u))
	}

	// Absolute redirect pointing to the target itself
	if originURL != nil && strings.EqualFold(u.Host, originURL.Host) {
		path := u.Path
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		return fmt.Sprintf("/b/%s%s%s", targetOrigin, path, queryAndFragment(u))
	}

	return loc
}

func queryAndFragment(u *url.URL) string {
	res := ""
	if u.RawQuery != "" {
		res += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		res += "#" + u.Fragment
	}
	return res
}

func sanitizeBrowserCookie(rawCookie string) string {
	parts := strings.Split(rawCookie, ";")
	var newParts []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "domain=") {
			continue
		}
		if strings.HasPrefix(lower, "path=") {
			newParts = append(newParts, "Path=/")
			continue
		}
		if strings.HasPrefix(lower, "samesite=") {
			newParts = append(newParts, "SameSite=Lax")
			continue
		}
		newParts = append(newParts, trimmed)
	}
	return strings.Join(newParts, "; ")
}

// injectBrowserToolbarAndInterceptors injects a minimal floating toolbar and link interceptor into HTML
func injectBrowserToolbarAndInterceptors(htmlBytes []byte, targetOrigin string) []byte {
	script := []byte(fmt.Sprintf(`
<!-- Zero Reverse Proxy Browser Injections -->
<style id="zrp-style">
#zrp-toolbar {
  position: fixed; top: 12px; right: 12px; z-index: 2147483647;
  background: rgba(15, 23, 42, 0.92); color: #fff; backdrop-filter: blur(8px);
  padding: 6px 12px; border-radius: 9999px; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  font-size: 12px; display: flex; align-items: center; gap: 8px; box-shadow: 0 4px 16px rgba(0,0,0,0.25); border: 1px solid rgba(255,255,255,0.15);
}
#zrp-toolbar a, #zrp-toolbar button {
  color: #fff; background: none; border: none; cursor: pointer; text-decoration: none; font-size: 11px; padding: 2px 6px; border-radius: 4px;
}
#zrp-toolbar a:hover, #zrp-toolbar button:hover { background: rgba(255,255,255,0.2); }
#zrp-toolbar .badge { background: #4f46e5; padding: 2px 8px; border-radius: 9999px; font-weight: 600; font-size: 10px; }
</style>
<div id="zrp-toolbar">
  <span class="badge">Zero Proxy</span>
  <span style="opacity:0.8;max-width:180px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;" title="%s">%s</span>
  <a href="/admin/" target="_blank" title="打开控制台">控制台</a>
  <button onclick="document.getElementById('zrp-toolbar').remove();" title="隐藏此浮条">✕</button>
</div>
<script id="zrp-interceptor">
(() => {
  const origin = "%s";
  document.addEventListener("click", (e) => {
    const a = e.target.closest("a");
    if (!a || !a.href) return;
    try {
      const u = new URL(a.href, window.location.href);
      if (u.origin === window.location.origin) {
        if (!u.pathname.startsWith("/b/")) {
          e.preventDefault();
          window.location.href = "/b/" + origin + u.pathname + u.search + u.hash;
        }
      } else if (u.origin === origin) {
        e.preventDefault();
        window.location.href = "/b/" + origin + u.pathname + u.search + u.hash;
      }
    } catch (err) {}
  }, true);
})();
</script>
`, targetOrigin, targetOrigin, targetOrigin))

	// Insert right before </body> or append
	lowerHTML := bytes.ToLower(htmlBytes)
	idx := bytes.Index(lowerHTML, []byte("</body>"))
	if idx != -1 {
		var buf bytes.Buffer
		buf.Grow(len(htmlBytes) + len(script))
		buf.Write(htmlBytes[:idx])
		buf.Write(script)
		buf.Write(htmlBytes[idx:])
		return buf.Bytes()
	}
	return append(htmlBytes, script...)
}
