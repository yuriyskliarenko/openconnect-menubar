package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
)

const appID = "openconnect-menubar"

// Connection holds everything about a VPN profile except its secrets, which
// live in the Keychain. It is safe to read connections.json in plain text.
type Connection struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Server        string `json:"server"`
	Protocol      string `json:"protocol"`
	Username      string `json:"username,omitempty"`
	AuthGroup     string `json:"authGroup,omitempty"`
	Certificate   string `json:"certificate,omitempty"`
	CertEncrypted bool   `json:"certEncrypted,omitempty"`
	NeedsPassword bool   `json:"needsPassword"`
	ServerCert    string `json:"serverCert,omitempty"`
	SearchDomain  string `json:"searchDomain,omitempty"`
	BaseMTU       int    `json:"baseMtu,omitempty"`
	NoDTLS        bool   `json:"noDtls,omitempty"`
	ExtraArgs     string `json:"extraArgs,omitempty"`
}

var (
	protocols = []string{"anyconnect", "nc", "gp", "pulse", "f5", "fortinet", "array"}

	serverRe = regexp.MustCompile(`^[A-Za-z0-9\[][A-Za-z0-9.:_/\[\]-]*$`)
	pinRe    = regexp.MustCompile(`^[A-Za-z0-9:+/=_-]+$`)
	// searchDomain is interpolated into a shell command run as root, so this
	// pattern must never admit anything a shell treats specially.
	domainRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*$`)
)

func (c *Connection) validate() error {
	c.Name = strings.TrimSpace(c.Name)
	c.Server = strings.TrimSpace(c.Server)
	c.Username = strings.TrimSpace(c.Username)
	c.AuthGroup = strings.TrimSpace(c.AuthGroup)
	c.Certificate = strings.TrimSpace(c.Certificate)
	c.ServerCert = strings.TrimSpace(c.ServerCert)
	c.SearchDomain = strings.TrimSpace(c.SearchDomain)
	c.ExtraArgs = strings.TrimSpace(c.ExtraArgs)
	if c.Protocol == "" {
		c.Protocol = "anyconnect"
	}
	switch {
	case c.Name == "":
		return errors.New("name is required")
	case !serverRe.MatchString(c.Server):
		return errors.New("server must be a host name, IP address or URL")
	case !slices.Contains(protocols, c.Protocol):
		return fmt.Errorf("unknown protocol %q", c.Protocol)
	case c.ServerCert != "" && !pinRe.MatchString(c.ServerCert):
		return errors.New("server certificate pin has an invalid format")
	case c.SearchDomain != "" && !domainRe.MatchString(c.SearchDomain):
		return errors.New("search domain must be a plain domain name")
	case c.BaseMTU != 0 && (c.BaseMTU < 576 || c.BaseMTU > 9000):
		return errors.New("base MTU must be between 576 and 9000")
	}
	if c.Certificate != "" {
		if !filepath.IsAbs(c.Certificate) {
			return errors.New("certificate must be an absolute path")
		}
		if _, err := os.Stat(c.Certificate); err != nil {
			return errors.New("certificate file not found")
		}
	} else {
		c.CertEncrypted = false
	}
	return nil
}

// Store is the on-disk list of connections.
type Store struct {
	mu       sync.Mutex
	dir      string
	conns    []Connection
	onChange func()
}

func configDir() (string, error) {
	if d := os.Getenv("OCM_CONFIG_DIR"); d != "" {
		return d, nil
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, appID), nil
}

func loadStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir}
	data, err := os.ReadFile(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.conns); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path(), err)
	}
	return s, nil
}

func (s *Store) path() string { return filepath.Join(s.dir, "connections.json") }

func (s *Store) List() []Connection {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.conns)
}

func (s *Store) Get(id string) (Connection, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.conns, func(c Connection) bool { return c.ID == id })
	if i < 0 {
		return Connection{}, false
	}
	return s.conns[i], true
}

// Save adds c, or replaces the connection with the same ID, and returns the
// stored value.
func (s *Store) Save(c Connection) (Connection, error) {
	if err := c.validate(); err != nil {
		return c, err
	}
	s.mu.Lock()
	next := slices.Clone(s.conns)
	dup := slices.ContainsFunc(next, func(o Connection) bool {
		return o.ID != c.ID && strings.EqualFold(o.Name, c.Name)
	})
	if dup {
		s.mu.Unlock()
		return c, fmt.Errorf("a connection named %q already exists", c.Name)
	}
	if i := slices.IndexFunc(next, func(o Connection) bool { return o.ID == c.ID }); c.ID != "" && i >= 0 {
		next[i] = c
	} else {
		c.ID = newID()
		next = append(next, c)
	}
	err := s.commit(next)
	s.mu.Unlock()
	if err == nil {
		s.changed()
	}
	return c, err
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	next := slices.DeleteFunc(slices.Clone(s.conns), func(c Connection) bool { return c.ID == id })
	err := s.commit(next)
	s.mu.Unlock()
	if err == nil {
		s.changed()
	}
	return err
}

// commit writes via a temp file so a crash can't truncate the config.
func (s *Store) commit(next []Connection) error {
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path() + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path()); err != nil {
		return err
	}
	s.conns = next
	return nil
}

func (s *Store) changed() {
	if s.onChange != nil {
		s.onChange()
	}
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
