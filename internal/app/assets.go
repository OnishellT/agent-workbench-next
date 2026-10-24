package app

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

func (s *Server) serveAssets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	if !s.assetsOK {
		writeError(w, http.StatusServiceUnavailable, "ui_not_implemented")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	if strings.ContainsAny(name, "\\\x00") || !fs.ValidPath(name) || hasHiddenSegment(name) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	data, err := fs.ReadFile(s.assets, name)
	if err != nil && path.Ext(name) == "" {
		name = "index.html"
		data, err = fs.ReadFile(s.assets, name)
	}
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	// Only the shell document declares the policy: assets, the API and the native sites
	// keep their own headers instead of a second CSP from this layer.
	if strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		w.Header().Set("Content-Security-Policy", shellPolicy)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func hasHiddenSegment(name string) bool {
	for _, segment := range strings.Split(name, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.HasPrefix(segment, ".") {
			return true
		}
	}
	return false
}

// shellPolicy confines the shell document to this origin. It deliberately omits
// default-src and frame-src: the per-host Magpie and SIM panels keep the origin
// validation this app already performs instead of a second static allow-list. Inline
// styles stay allowed because the terminal injects its own stylesheet and the widgets
// set style attributes; scripts stay 'self' with no eval.
const shellPolicy = "script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self'; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'self'"
