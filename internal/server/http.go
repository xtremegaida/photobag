package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"photobag/internal/decks"
	"photobag/internal/experiments"
	"photobag/internal/files"
	"photobag/internal/library"
	"photobag/internal/reencode"
	"photobag/internal/scoring"
	"photobag/internal/webui"
)

// Handler builds the full HTTP handler (API + UI) with security layers.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	mux.Handle("/", s.static())

	cop := http.NewCrossOriginProtection()
	if s.cfg.Dev {
		for _, o := range []string{"http://localhost:5173", "http://127.0.0.1:5173"} {
			_ = cop.AddTrustedOrigin(o)
		}
	}
	return s.hostCheck(s.auth(cop.Handler(mux)))
}

// hostCheck rejects requests whose Host header is not a loopback name,
// defeating DNS-rebinding attacks from web pages.
func (s *Server) hostCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.AllowRemote {
			host := r.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			host = strings.Trim(strings.ToLower(host), "[]")
			if host != "localhost" && host != "127.0.0.1" && host != "::1" && !strings.HasSuffix(host, ".localhost") {
				http.Error(w, "PhotoBag only answers on localhost (start with --allow-remote for LAN access)", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) cookieName() string { return "photobag_token_" + strconv.Itoa(s.port) }

func (s *Server) validToken(t string) bool {
	return subtle.ConstantTimeCompare([]byte(t), []byte(s.token)) == 1
}

func (s *Server) setCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName(), Value: s.token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
}

// auth enforces the per-launch token (cookie, header, or ?token= which is
// exchanged for a cookie).
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.NoToken {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/auth" && r.Method == http.MethodPost {
			var body struct {
				Token string `json:"token"`
			}
			_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
			if !s.validToken(body.Token) {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
				return
			}
			s.setCookie(w)
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
			return
		}
		if c, err := r.Cookie(s.cookieName()); err == nil && s.validToken(c.Value) {
			next.ServeHTTP(w, r)
			return
		}
		if s.validToken(r.Header.Get("X-PhotoBag-Token")) {
			next.ServeHTTP(w, r)
			return
		}
		if t := r.URL.Query().Get("token"); t != "" && s.validToken(t) && r.Method == http.MethodGet {
			s.setCookie(w)
			q := r.URL.Query()
			q.Del("token")
			u := *r.URL
			u.RawQuery = q.Encode()
			http.Redirect(w, r, u.RequestURI(), http.StatusSeeOther)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid access token"})
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `<!doctype html><title>PhotoBag</title><body style="font-family:sans-serif;padding:3em">
<h1>PhotoBag</h1><p>Open the link printed by <code>photobag serve</code> (it contains an access token).</p></body>`)
	})
}

// static serves the embedded SPA with an index.html fallback.
func (s *Server) static() http.Handler {
	files := webui.FS()
	fileServer := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, apiErr(http.StatusNotFound, "unknown API endpoint %s %s", r.Method, r.URL.Path))
			return
		}
		if s.cfg.Dev || !webui.Built() {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			msg := "The web UI is not built into this binary. Run <code>node scripts/build.mjs</code>."
			if s.cfg.Dev {
				msg = "Dev mode: open the Vite dev server at <a href=\"http://localhost:5173/\">http://localhost:5173/</a>."
			}
			fmt.Fprintf(w, `<!doctype html><title>PhotoBag</title><body style="font-family:sans-serif;padding:3em"><h1>PhotoBag</h1><p>%s</p></body>`, msg)
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if st, err := fs.Stat(files, p); err == nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(files, "index.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		page := strings.Replace(string(index), "<head>", `<head><meta name="photobag-bag" content="`+s.bagTag()+`">`, 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		io.WriteString(w, page)
	})
}

// apiError carries an HTTP status.
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func apiErr(status int, format string, args ...any) error {
	return &apiError{status: status, msg: fmt.Sprintf(format, args...)}
}

func badRequest(err error) error { return &apiError{status: http.StatusBadRequest, msg: err.Error()} }

type handlerFunc func(w http.ResponseWriter, r *http.Request) error

func (s *Server) handle(mux *http.ServeMux, pattern string, h handlerFunc) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			writeError(w, err)
		}
	})
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	var ae *apiError
	switch {
	case errors.As(err, &ae):
		status = ae.status
	case errors.Is(err, library.ErrNotFound), errors.Is(err, scoring.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, files.ErrChanged):
		status = http.StatusPreconditionFailed
	case errors.Is(err, scoring.ErrConflict), errors.Is(err, experiments.ErrConflict), errors.Is(err, decks.ErrConflict),
		errors.Is(err, files.ErrConflict),
		errors.Is(err, reencode.ErrBusy):
		status = http.StatusConflict
	case errors.Is(err, experiments.ErrInvalid), errors.Is(err, decks.ErrInvalid),
		errors.Is(err, files.ErrInvalid),
		errors.Is(err, reencode.ErrInvalid):
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter, v any) error {
	writeJSON(w, http.StatusOK, v)
	return nil
}

// readJSON decodes a request body (up to 64 MB, for large id lists).
func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<20))
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return apiErr(http.StatusBadRequest, "invalid JSON body: %v", err)
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, apiErr(http.StatusBadRequest, "invalid %s", name)
	}
	return id, nil
}

// sse streams broker events until the client or server goes away.
func (s *Server) sse(w http.ResponseWriter, r *http.Request) error {
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	ch, unsub := s.events.Subscribe()
	defer unsub()
	// The bag's tag lets a page left open while another bag was served
	// on the same address notice and reload.
	fmt.Fprintf(w, "retry: 2000\ndata: {\"type\":\"hello\",\"data\":{\"bag\":%q}}\n\n", s.bagTag())
	if err := rc.Flush(); err != nil {
		return nil
	}
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-s.ctx.Done():
			return nil
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
		case ev, open := <-ch:
			if !open {
				return nil
			}
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
		if err := rc.Flush(); err != nil {
			return nil
		}
	}
}
