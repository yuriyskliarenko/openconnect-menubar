package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	killBin        = "/bin/kill"
	connectTimeout = 90 * time.Second
)

var errCancelled = errors.New("cancelled")

var (
	openconnectPaths = []string{"/opt/homebrew/bin/openconnect", "/usr/local/bin/openconnect", "/opt/local/sbin/openconnect", "/opt/local/bin/openconnect"}
	vpncScriptPaths  = []string{"/opt/local/etc/vpnc-scripts/vpnc-script", "/opt/homebrew/etc/vpnc/vpnc-script", "/usr/local/etc/vpnc/vpnc-script"}
)

// System describes the local openconnect installation.
type System struct {
	Openconnect string `json:"openconnect"`
	VpncScript  string `json:"vpncScript"`
	SudoOK      bool   `json:"sudoOk"`
	SudoersLine string `json:"sudoersLine"`
}

func sudoAllowed(args ...string) bool {
	return exec.Command("/usr/bin/sudo", append([]string{"-n", "-l"}, args...)...).Run() == nil
}

// detectSystem prefers a binary that sudo may already run without a password,
// since several installs can coexist.
func detectSystem() System {
	var s System
	for _, p := range openconnectPaths {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if s.Openconnect == "" {
			s.Openconnect = p
		}
		if sudoAllowed(p) {
			s.Openconnect = p
			break
		}
	}
	for _, p := range vpncScriptPaths {
		if _, err := os.Stat(p); err == nil {
			s.VpncScript = p
			break
		}
	}
	if s.Openconnect != "" {
		s.SudoOK = sudoAllowed(s.Openconnect) && sudoAllowed(killBin)
		name := "YOUR_USER"
		if u, err := user.Current(); err == nil {
			name = u.Username
		}
		s.SudoersLine = fmt.Sprintf("%s ALL=(root) NOPASSWD: %s, %s", name, s.Openconnect, killBin)
	}
	return s
}

// buildArgs returns the openconnect command line. Secrets must never appear
// here: arguments are visible to every local user via ps.
func buildArgs(c Connection, sys System, pidFile string) ([]string, error) {
	args := []string{
		"--protocol=" + c.Protocol,
		"--background",
		"--pid-file=" + pidFile,
	}
	add := func(flag, v string) {
		if v != "" {
			args = append(args, flag+"="+v)
		}
	}
	add("--user", c.Username)
	add("--authgroup", c.AuthGroup)
	add("--certificate", c.Certificate)
	add("--servercert", c.ServerCert)
	if c.BaseMTU != 0 {
		add("--base-mtu", strconv.Itoa(c.BaseMTU))
	}
	if c.NoDTLS {
		args = append(args, "--no-dtls")
	}
	switch {
	case c.SearchDomain != "":
		if sys.VpncScript == "" {
			return nil, errors.New("a search domain requires vpnc-script, which was not found")
		}
		// --script is a shell command line; validate() restricts SearchDomain.
		add("--script", "CISCO_DEF_DOMAIN="+c.SearchDomain+" exec "+sys.VpncScript)
	case sys.VpncScript != "":
		add("--script", sys.VpncScript)
	}
	args = append(args, strings.Fields(c.ExtraArgs)...)
	return append(args, c.Server), nil
}

// stdinFor builds what openconnect reads when it prompts. The certificate is
// unlocked during the TLS handshake, before the server asks for a password,
// so the order here matches the order of the prompts.
func stdinFor(c Connection, certPass, loginPass string) string {
	var b strings.Builder
	if c.CertEncrypted {
		b.WriteString(certPass + "\n")
	}
	if c.NeedsPassword {
		b.WriteString(loginPass + "\n")
	}
	return b.String()
}

// Runner starts and stops the single openconnect process.
type Runner struct {
	dir     string
	logPath string
	mu      sync.Mutex // serialises connect and disconnect
}

func newRunner(dir string) *Runner {
	logs := dir
	if home, err := os.UserHomeDir(); err == nil && os.Getenv("OCM_CONFIG_DIR") == "" {
		logs = filepath.Join(home, "Library/Logs")
	}
	return &Runner{dir: dir, logPath: filepath.Join(logs, appID+".log")}
}

func (r *Runner) pidFile() string    { return filepath.Join(r.dir, "openconnect.pid") }
func (r *Runner) activeFile() string { return filepath.Join(r.dir, "active") }

func (r *Runner) pid() int {
	data, err := os.ReadFile(r.pidFile())
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0
	}
	// comm, not args: a stale pid file may name a recycled, unrelated pid.
	out, err := exec.Command("/bin/ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil || filepath.Base(strings.TrimSpace(string(out))) != "openconnect" {
		return 0
	}
	return pid
}

// Active returns the ID of the running connection, or "" if none is up.
func (r *Runner) Active() string {
	if r.pid() == 0 {
		return ""
	}
	id, err := os.ReadFile(r.activeFile())
	if err != nil {
		return "?"
	}
	return strings.TrimSpace(string(id))
}

func (r *Runner) log() (*os.File, error) {
	return os.OpenFile(r.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
}

func waitFor(cond func() bool, d time.Duration) bool {
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return cond()
}

func (r *Runner) Disconnect() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.disconnect()
}

func (r *Runner) disconnect() error {
	pid := r.pid()
	if pid == 0 {
		return nil
	}
	f, err := r.log()
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Fprintf(f, "\n=== %s disconnect\n", time.Now().Format(time.DateTime))
	cmd := exec.Command("/usr/bin/sudo", "-n", killBin, strconv.Itoa(pid))
	cmd.Stdout, cmd.Stderr = f, f
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("could not stop openconnect: %w", err)
	}
	if !waitFor(func() bool { return r.pid() == 0 }, 10*time.Second) {
		return errors.New("openconnect did not stop")
	}
	return nil
}

// Connect replaces any running connection with c.
func (r *Runner) Connect(c Connection) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	sys := detectSystem()
	if sys.Openconnect == "" {
		return errors.New("openconnect is not installed")
	}
	if !sys.SudoOK {
		return errors.New("sudo is not configured, see Manage Connections")
	}
	certPass, loginPass, err := secretsFor(c)
	if err != nil {
		return err
	}
	if err := r.disconnect(); err != nil {
		return err
	}
	args, err := buildArgs(c, sys, r.pidFile())
	if err != nil {
		return err
	}
	f, err := r.log()
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Fprintf(f, "\n=== %s connect %s\n", time.Now().Format(time.DateTime), c.Name)
	if err := os.WriteFile(r.activeFile(), []byte(c.ID), 0o600); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sudo", append([]string{"-n", sys.Openconnect}, args...)...)
	cmd.Stdin = strings.NewReader(stdinFor(c, certPass, loginPass))
	// A file rather than a pipe: the daemonized openconnect would otherwise
	// hold a pipe open and Run would never return.
	cmd.Stdout, cmd.Stderr = f, f
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("openconnect failed: %w", err)
	}
	if !waitFor(func() bool { return r.pid() != 0 }, 5*time.Second) {
		return errors.New("openconnect did not start")
	}
	return nil
}

func secretsFor(c Connection) (cert, login string, err error) {
	if c.CertEncrypted {
		if cert, err = secret(c, secretCert, "Certificate passphrase"); err != nil {
			return "", "", err
		}
	}
	if c.NeedsPassword {
		if login, err = secret(c, secretLogin, "Password"); err != nil {
			return "", "", err
		}
	}
	return cert, login, nil
}

func secret(c Connection, kind, label string) (string, error) {
	if v, ok := keychainGet(c.ID, kind); ok {
		return v, nil
	}
	return askSecret(label, c.Name)
}

// askSecret shows a native dialog. Text goes through argv, never into the
// AppleScript source, so a connection name can't inject script.
func askSecret(label, name string) (string, error) {
	const script = `on run argv
	return text returned of (display dialog (item 1 of argv & " for " & item 2 of argv & ":") default answer "" with hidden answer with title "OpenConnect Menubar")
end run`
	out, err := exec.Command("/usr/bin/osascript", "-e", script, label, name).Output()
	if err != nil {
		return "", errCancelled
	}
	return strings.TrimRight(string(out), "\n"), nil
}
