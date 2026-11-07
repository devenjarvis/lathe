package desktop

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
)

// followRedirects makes the reader's redirects work in the app window. Wails
// answers the webview from a custom URL scheme, and webviews don't follow a
// 3xx from a scheme handler: WebKitGTK shows the "Found." body instead
// (seen with /{slug}/ -> /{slug}/part-01.md, and the delete form's 303 back
// to /). So a same-origin redirect is rewritten into a 200 page that
// replaces itself with the target, which leaves no extra history entry.
// Anything else, including SSE streams (Flush passes through), is untouched.
// Only the asset handler is wrapped: browsers and the CLI on the loopback
// listener follow redirects themselves.
func followRedirects(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(&redirectWriter{ResponseWriter: w}, r)
	})
}

type redirectWriter struct {
	http.ResponseWriter
	wroteHeader bool
	swallow     bool // the redirect page is written; drop the handler's body
}

func (w *redirectWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	if code >= 300 && code < 400 {
		if loc := w.Header().Get("Location"); loc != "" {
			if target, err := windowPath(loc); err == nil {
				w.writeRedirectPage(target)
				return
			}
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *redirectWriter) writeRedirectPage(target string) {
	w.swallow = true
	h := w.Header()
	h.Del("Location")
	h.Del("Content-Length")
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.ResponseWriter.WriteHeader(http.StatusOK)
	js, _ := json.Marshal(target) // escapes <, > and &, so safe inside <script>
	esc := html.EscapeString(target)
	_, _ = fmt.Fprintf(w.ResponseWriter, `<!doctype html><meta charset="utf-8">`+
		`<meta http-equiv="refresh" content="0;url=%s">`+
		`<script>location.replace(%s)</script>`+
		`<a href="%s">Continue</a>`, esc, js, esc)
}

func (w *redirectWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.swallow {
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}

// Flush keeps SSE streaming through the wrapper.
func (w *redirectWriter) Flush() {
	if w.swallow {
		return
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *redirectWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
