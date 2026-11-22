package main

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"testing/fstest"
	"time"
)

func TestDesktopMermaidBrowser(t *testing.T) {
	if os.Getenv("SPOOL_MERMAID_BROWSER") != "1" {
		t.Skip("set SPOOL_MERMAID_BROWSER=1 for the desktop Mermaid browser check")
	}
	const mount = "/web/fixture/"
	files := fstest.MapFS{}
	for name, source := range map[string]string{
		"assets/mermaid.min-fixture.js":   "../../web/node_modules/mermaid/dist/mermaid.min.js",
		"assets/mermaid-frame-fixture.js": "../../web/src/mermaid-frame.js",
	} {
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: data}
	}
	// Exercise the real gateway using an opaque sandbox and the production
	// script policy. Diagram text is sent by the browser driver as a message.
	document := `<div id="diagram"></div><div id="scratch"></div><script src="assets/mermaid.min-fixture.js"></script><script src="assets/mermaid-frame-fixture.js"></script>`
	files["index.html"] = &fstest.MapFile{Data: []byte(`<!doctype html><iframe title="Mermaid" sandbox="allow-scripts" style="width:800px;height:600px" srcdoc="` + html.EscapeString(document) + `"></iframe>`)}
	gateway := &desktopServerGateway{
		assets: http.FileServer(http.FS(files)),
		routes: map[string]http.Handler{mount: http.NotFoundHandler()},
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src data:; base-uri 'none'")
		gateway.ServeHTTP(w, r)
	}))
	gateway.url = "http://" + server.Listener.Addr().String()
	server.Start()
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bun", "testdata/mermaid-browser.mjs", gateway.url+mount)
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("desktop Mermaid browser check: %v\n%s", err, output)
	}
	t.Log(string(output))
}
