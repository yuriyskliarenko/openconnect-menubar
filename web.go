package main

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
)

//go:embed web/index.html
var indexHTML string

// connectionView is a Connection plus which secrets exist, never their values.
type connectionView struct {
	Connection
	HasLogin bool `json:"hasLogin"`
	HasCert  bool `json:"hasCert"`
}

type saveRequest struct {
	Connection    Connection `json:"connection"`
	LoginPassword string     `json:"loginPassword"`
	CertPassword  string     `json:"certPassword"`
	ClearLogin    bool       `json:"clearLogin"`
	ClearCert     bool       `json:"clearCert"`
}

// Web is the connection manager, served on loopback only. Its API ends up
// running openconnect as root, so every request must carry the random token
// and a loopback Host header, which stops other sites and DNS rebinding.
type Web struct {
	store  *Store
	runner *Runner
	// act runs connect/disconnect through the same path as the menu.
	act   func(label string, fn func() error)
	token string
	addr  string
}

func startWeb(store *Store, runner *Runner, act func(string, func() error)) (*Web, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	w := &Web{store: store, runner: runner, act: act, token: hex.EncodeToString(b), addr: ln.Addr().String()}
	go http.Serve(ln, w.handler())
	return w, nil
}

func (w *Web) URL() string { return fmt.Sprintf("http://%s/?token=%s", w.addr, w.token) }

func (w *Web) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", w.index)
	api := http.NewServeMux()
	api.HandleFunc("GET /api/state", w.state)
	api.HandleFunc("POST /api/connections", w.save)
	api.HandleFunc("DELETE /api/connections/{id}", w.remove)
	api.HandleFunc("POST /api/connect/{id}", w.connect)
	api.HandleFunc("POST /api/disconnect", w.disconnect)
	api.HandleFunc("POST /api/pick-certificate", w.pickCertificate)
	mux.Handle("/api/", w.authed(api))
	return w.hostCheck(mux)
}

func (w *Web) hostCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Host != w.addr {
			http.Error(rw, "forbidden", http.StatusForbidden)
			return
		}
		rw.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'")
		rw.Header().Set("X-Frame-Options", "DENY")
		rw.Header().Set("Referrer-Policy", "no-referrer")
		rw.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(rw, r)
	})
}

func (w *Web) tokenOK(got string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(w.token)) == 1
}

func (w *Web) authed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if !w.tokenOK(r.Header.Get("X-Token")) {
			http.Error(rw, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(rw, r)
	})
}

func (w *Web) index(rw http.ResponseWriter, r *http.Request) {
	if !w.tokenOK(r.URL.Query().Get("token")) {
		http.Error(rw, "forbidden: open this page from the menu bar", http.StatusForbidden)
		return
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.Write([]byte(strings.Replace(indexHTML, "__TOKEN__", w.token, 1)))
}

func writeJSON(rw http.ResponseWriter, v any) {
	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(v)
}

func writeError(rw http.ResponseWriter, status int, err error) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	json.NewEncoder(rw).Encode(map[string]string{"error": err.Error()})
}

func (w *Web) state(rw http.ResponseWriter, r *http.Request) {
	conns := w.store.List()
	views := make([]connectionView, len(conns))
	for i, c := range conns {
		views[i] = connectionView{c, keychainHas(c.ID, secretLogin), keychainHas(c.ID, secretCert)}
	}
	writeJSON(rw, map[string]any{
		"connections": views,
		"activeId":    w.runner.Active(),
		"system":      detectSystem(),
		"protocols":   protocols,
	})
}

func (w *Web) save(rw http.ResponseWriter, r *http.Request) {
	var req saveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(rw, http.StatusBadRequest, err)
		return
	}
	for _, s := range []string{req.LoginPassword, req.CertPassword} {
		if strings.ContainsAny(s, "\r\n") {
			writeError(rw, http.StatusBadRequest, errors.New("passwords can't contain line breaks"))
			return
		}
	}
	c, err := w.store.Save(req.Connection)
	if err != nil {
		writeError(rw, http.StatusBadRequest, err)
		return
	}
	apply := func(kind, value string, clear bool) error {
		switch {
		case value != "":
			return keychainSet(c.ID, kind, value)
		case clear:
			keychainDelete(c.ID, kind)
		}
		return nil
	}
	if err := errors.Join(
		apply(secretLogin, req.LoginPassword, req.ClearLogin),
		apply(secretCert, req.CertPassword, req.ClearCert),
	); err != nil {
		writeError(rw, http.StatusInternalServerError, err)
		return
	}
	writeJSON(rw, c)
}

func (w *Web) remove(rw http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == w.runner.Active() {
		writeError(rw, http.StatusConflict, errors.New("disconnect before deleting this connection"))
		return
	}
	if err := w.store.Delete(id); err != nil {
		writeError(rw, http.StatusInternalServerError, err)
		return
	}
	keychainDelete(id, secretLogin)
	keychainDelete(id, secretCert)
	rw.WriteHeader(http.StatusNoContent)
}

func (w *Web) connect(rw http.ResponseWriter, r *http.Request) {
	c, ok := w.store.Get(r.PathValue("id"))
	if !ok {
		writeError(rw, http.StatusNotFound, errors.New("no such connection"))
		return
	}
	w.act("Connecting to "+c.Name, func() error { return w.runner.Connect(c) })
	rw.WriteHeader(http.StatusAccepted)
}

func (w *Web) disconnect(rw http.ResponseWriter, r *http.Request) {
	w.act("Disconnecting", w.runner.Disconnect)
	rw.WriteHeader(http.StatusAccepted)
}

// pickCertificate uses a native dialog, since a browser never reveals the
// real path of a chosen file.
func (w *Web) pickCertificate(rw http.ResponseWriter, r *http.Request) {
	out, err := exec.Command("/usr/bin/osascript", "-e",
		`tell application "System Events"
	activate
	return POSIX path of (choose file with prompt "Choose a client certificate")
end tell`).Output()
	if err != nil {
		writeJSON(rw, map[string]string{"path": ""})
		return
	}
	writeJSON(rw, map[string]string{"path": strings.TrimSpace(string(out))})
}
