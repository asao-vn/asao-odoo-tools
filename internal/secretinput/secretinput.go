// Package secretinput asks the person for one secret on a one-time page on their own machine
// (DEC-2026-0179). The value goes from their keyboard to the caller, never through an agent's chat.
//
// The page lives on 127.0.0.1, on a random port, under a random path, for one submission and at most
// Timeout. It loads nothing from elsewhere, refuses a form without its own token or from another
// origin, and never shows the value back.
package secretinput

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// DefaultTimeout is how long the page waits for the person.
const DefaultTimeout = 5 * time.Minute

const maxValueBytes = 16 << 10

var (
	// ErrTimeout: the person did not submit in time.
	ErrTimeout = errors.New("secretinput: the person did not enter the secret in time")
	// ErrEmpty: the form was submitted without a value.
	ErrEmpty = errors.New("secretinput: an empty value was submitted")
)

// Request says what the page asks for.
type Request struct {
	// Title names what is being set, e.g. "API key for profile shop".
	Title string
	// Detail says where it goes, e.g. "Odoo at https://erp.example.com, stored in the keyring".
	Detail string
	// Label is the field's label.
	Label string
	// Theme is "simple" (the default: colours that follow the system's light or dark mode) or "plain".
	Theme string
	// Timeout is how long to wait; zero means DefaultTimeout.
	Timeout time.Duration
	// Announce tells the person where the page is (it is opened in their browser as well).
	Announce func(address string)
}

// OpenBrowser opens an address in the person's browser. A test replaces it.
var OpenBrowser = func(address string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", address)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", address)
	default:
		command = exec.Command("xdg-open", address)
	}
	return command.Start()
}

func randomHex(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

// Collect serves the page, opens it, and returns the value the person submits.
func Collect(ctx context.Context, request Request) (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("secretinput: cannot listen on 127.0.0.1: %w", err)
	}
	path, err := randomHex(32)
	if err != nil {
		listener.Close()
		return "", err
	}
	formToken, err := randomHex(32)
	if err != nil {
		listener.Close()
		return "", err
	}
	origin := "http://" + listener.Addr().String()
	address := origin + "/" + path
	values := make(chan string, 1)
	done := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/"+path, func(w http.ResponseWriter, r *http.Request) {
		secureHeaders(w)
		switch r.Method {
		case http.MethodGet:
			select {
			case <-done:
				http.Error(w, "This page has been used.", http.StatusGone)
				return
			default:
			}
			_, _ = w.Write([]byte(page(request, formToken)))
		case http.MethodPost:
			if !sameOrigin(r, origin) {
				http.Error(w, "Refused: the form came from another site.", http.StatusForbidden)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxValueBytes)
			if err := r.ParseForm(); err != nil {
				http.Error(w, "Refused: the form could not be read.", http.StatusBadRequest)
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.PostForm.Get("token")), []byte(formToken)) != 1 {
				http.Error(w, "Refused: this form is not the page's own.", http.StatusForbidden)
				return
			}
			value := strings.TrimRight(r.PostForm.Get("value"), "\r\n")
			if value == "" {
				_, _ = w.Write([]byte(page(request, formToken) + ""))
				return
			}
			select {
			case values <- value:
				close(done)
				_, _ = w.Write([]byte(savedPage(request)))
			default:
				http.Error(w, "This page has been used.", http.StatusGone)
			}
		default:
			http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
		}
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	if request.Announce != nil {
		request.Announce(address)
	}
	_ = OpenBrowser(address)
	timeout := request.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case value := <-values:
		return value, nil
	case <-timer.C:
		return "", ErrTimeout
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// sameOrigin accepts a request whose Origin, when the browser sends one, is the page's own.
func sameOrigin(r *http.Request, origin string) bool {
	if sent := r.Header.Get("Origin"); sent != "" && sent != origin {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	return true
}

func secureHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
}

func style(theme string) string {
	if theme == "plain" {
		return `body{font-family:system-ui,sans-serif;max-width:32rem;margin:3rem auto;padding:0 1rem}input,button{font:inherit;padding:.5rem;width:100%;box-sizing:border-box;margin-top:.5rem}`
	}
	return `:root{color-scheme:light dark;--bg:#fafafa;--fg:#1d1d1f;--muted:#6e6e73;--card:#fff;--line:#d2d2d7;--accent:#0a66c2}
@media (prefers-color-scheme:dark){:root{--bg:#161617;--fg:#f5f5f7;--muted:#a1a1a6;--card:#1d1d1f;--line:#3a3a3c;--accent:#4d9fff}}
body{background:var(--bg);color:var(--fg);font-family:system-ui,-apple-system,"Segoe UI",sans-serif;margin:0;padding:3rem 1rem}
main{max-width:30rem;margin:0 auto;background:var(--card);border:1px solid var(--line);border-radius:12px;padding:1.5rem}
h1{font-size:1.15rem;margin:0 0 .25rem}p{color:var(--muted);margin:.25rem 0 1rem;line-height:1.4}
label{display:block;font-weight:600;margin-top:.5rem}
input{width:100%;box-sizing:border-box;padding:.6rem .7rem;margin-top:.4rem;border:1px solid var(--line);border-radius:8px;background:transparent;color:inherit;font:inherit}
button{margin-top:1rem;width:100%;padding:.65rem;border:0;border-radius:8px;background:var(--accent);color:#fff;font:inherit;font-weight:600;cursor:pointer}
small{display:block;color:var(--muted);margin-top:1rem}`
}

func page(request Request, token string) string {
	label := request.Label
	if label == "" {
		label = "Secret"
	}
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>` + html.EscapeString(request.Title) + `</title><style>` + style(request.Theme) + `</style></head><body><main>` +
		`<h1>` + html.EscapeString(request.Title) + `</h1><p>` + html.EscapeString(request.Detail) + `</p>` +
		`<form method="post" autocomplete="off"><input type="hidden" name="token" value="` + token + `">` +
		`<label for="value">` + html.EscapeString(label) + `</label>` +
		`<input id="value" name="value" type="password" autofocus required spellcheck="false">` +
		`<button type="submit">Save</button></form>` +
		`<small>This page is on your own computer and works once. The value goes to the store, never to the agent or its chat.</small>` +
		`</main></body></html>`
}

func savedPage(request Request) string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>Saved</title><style>` + style(request.Theme) + `</style></head><body><main><h1>Saved</h1>` +
		`<p>` + html.EscapeString(request.Title) + ` is stored. You can close this tab.</p></main></body></html>`
}
