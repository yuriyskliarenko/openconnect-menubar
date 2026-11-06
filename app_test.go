package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func valid() Connection {
	return Connection{Name: "Work", Server: "vpn.example.com", NeedsPassword: true}
}

func TestValidateRejectsUnsafeValues(t *testing.T) {
	cases := map[string]func(*Connection){
		"empty name":      func(c *Connection) { c.Name = " " },
		"option server":   func(c *Connection) { c.Server = "--script=/tmp/x" },
		"spaced server":   func(c *Connection) { c.Server = "a b" },
		"protocol":        func(c *Connection) { c.Protocol = "ssh" },
		"shell in domain": func(c *Connection) { c.SearchDomain = "a.com;id" },
		"subshell domain": func(c *Connection) { c.SearchDomain = "$(id).com" },
		"spaced domain":   func(c *Connection) { c.SearchDomain = "a.com id" },
		"pin":             func(c *Connection) { c.ServerCert = "a b" },
		"mtu":             func(c *Connection) { c.BaseMTU = 10 },
		"relative cert":   func(c *Connection) { c.Certificate = "a.p12" },
		"missing cert":    func(c *Connection) { c.Certificate = "/nonexistent/a.p12" },
	}
	for name, mutate := range cases {
		c := valid()
		mutate(&c)
		if c.validate() == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	c := valid()
	c.SearchDomain = "corp.example.local"
	c.ServerCert = "pin-sha256:AbC+/1="
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	if c.Protocol != "anyconnect" {
		t.Errorf("default protocol = %q", c.Protocol)
	}
}

func TestBuildArgs(t *testing.T) {
	c := valid()
	c.Protocol, c.Username, c.BaseMTU, c.NoDTLS = "gp", "me", 1100, true
	c.SearchDomain, c.ExtraArgs = "corp.local", "--no-xmlpost  --reconnect-timeout=60"
	args, err := buildArgs(c, System{VpncScript: "/x/vpnc-script"}, "/p.pid")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"--protocol=gp", "--user=me", "--base-mtu=1100", "--no-dtls", "--pid-file=/p.pid", "--background",
		"--script=CISCO_DEF_DOMAIN=corp.local exec /x/vpnc-script", "--no-xmlpost", "--reconnect-timeout=60",
	} {
		if !slices.Contains(args, want) {
			t.Errorf("missing %q in %q", want, args)
		}
	}
	if args[len(args)-1] != c.Server {
		t.Errorf("server must come last: %q", args)
	}
	if _, err := buildArgs(c, System{}, "/p.pid"); err == nil {
		t.Error("a search domain without vpnc-script must fail")
	}
}

func TestStdinOrder(t *testing.T) {
	c := valid()
	c.CertEncrypted = true
	if got := stdinFor(c, "cert", "login"); got != "cert\nlogin\n" {
		t.Errorf("got %q", got)
	}
	c.CertEncrypted = false
	if got := stdinFor(c, "", "login"); got != "login\n" {
		t.Errorf("got %q", got)
	}
	c.NeedsPassword = false
	if got := stdinFor(c, "", ""); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestStorePersists(t *testing.T) {
	dir := t.TempDir()
	s, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Save(valid())
	if err != nil || c.ID == "" {
		t.Fatalf("save: %v %+v", err, c)
	}
	if _, err := s.Save(valid()); err == nil {
		t.Error("a duplicate name must be rejected")
	}
	c.Server = "other.example.com"
	if _, err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	s2, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if l := s2.List(); len(l) != 1 || l[0].Server != "other.example.com" {
		t.Fatalf("reloaded %+v", l)
	}
	if err := s2.Delete(c.ID); err != nil || len(s2.List()) != 0 {
		t.Fatalf("delete: %v", err)
	}
}

func TestWebRequiresTokenAndLoopbackHost(t *testing.T) {
	dir := t.TempDir()
	s, _ := loadStore(dir)
	w := &Web{store: s, runner: newRunner(dir), token: "secret", addr: "127.0.0.1:1"}
	h := w.handler()
	cases := []struct {
		target, host, token string
		want                int
	}{
		{"/api/state", "127.0.0.1:1", "", http.StatusForbidden},
		{"/api/state", "127.0.0.1:1", "wrong", http.StatusForbidden},
		{"/api/state", "evil.example:1", "secret", http.StatusForbidden},
		{"/", "127.0.0.1:1", "", http.StatusForbidden},
		{"/?token=secret", "evil.example:1", "", http.StatusForbidden},
		{"/?token=secret", "127.0.0.1:1", "", http.StatusOK},
		{"/api/state", "127.0.0.1:1", "secret", http.StatusOK},
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", tc.target, nil)
		r.Host = tc.host
		r.Header.Set("X-Token", tc.token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != tc.want {
			t.Errorf("%s host=%s token=%q: got %d, want %d", tc.target, tc.host, tc.token, rec.Code, tc.want)
		}
	}
}

func TestSecretsNeverInConfig(t *testing.T) {
	dir := t.TempDir()
	s, _ := loadStore(dir)
	if _, err := s.Save(valid()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.path(), "..") || filepath.Dir(s.path()) != dir {
		t.Fatal("config escaped its directory")
	}
}
