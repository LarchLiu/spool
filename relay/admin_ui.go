package relay

import (
	"bytes"
	"embed"
	"html"
	"net/http"
)

//go:embed adminui/index.html adminui/admin.css adminui/admin.js adminui/presentation.js adminui/logo.png
var adminUI embed.FS

func serveAdminAsset(w http.ResponseWriter, r *http.Request) bool {
	var file, kind string
	switch r.URL.Path {
	case "/":
		file, kind = "index.html", "text/html; charset=utf-8"
	case "/admin.css":
		file, kind = "admin.css", "text/css; charset=utf-8"
	case "/admin.js":
		file, kind = "admin.js", "text/javascript; charset=utf-8"
	case "/logo.png":
		file, kind = "logo.png", "image/png"
	case "/presentation.js":
		file, kind = "presentation.js", "text/javascript; charset=utf-8"
	default:
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		adminError(w, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}
	data, _ := adminUI.ReadFile("adminui/" + file)
	if file == "index.html" {
		data = bytes.ReplaceAll(data, []byte("{{version}}"), []byte(html.EscapeString(Version())))
	}
	w.Header().Set("Content-Type", kind)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
	return true
}
