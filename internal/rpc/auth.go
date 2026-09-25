package rpc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Authenticator supplies HTTP basic credentials for an RPC call.
type Authenticator interface {
	Credentials() (user, pass string, err error)
	// Describe returns a human readable, secret-free summary for logging.
	Describe() string
}

// StaticAuth carries a fixed rpcuser/rpcpassword pair.
type StaticAuth struct {
	User string
	Pass string
}

func (a StaticAuth) Credentials() (string, string, error) { return a.User, a.Pass, nil }
func (a StaticAuth) Describe() string                     { return fmt.Sprintf("rpcuser %q", a.User) }

// CookieAuth reads bitcoind's .cookie file. The file is rewritten with fresh
// credentials every time the node restarts, so it is re-read whenever its
// modification time or size changes.
type CookieAuth struct {
	path string

	mu      sync.Mutex
	user    string
	pass    string
	modTime time.Time
	size    int64
	loaded  bool
}

// NewCookieAuth returns an Authenticator backed by the cookie file at path.
func NewCookieAuth(path string) *CookieAuth { return &CookieAuth{path: path} }

// Path returns the cookie file location.
func (a *CookieAuth) Path() string { return a.path }

func (a *CookieAuth) Describe() string { return fmt.Sprintf("cookie file %s", a.path) }

func (a *CookieAuth) Credentials() (string, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	info, err := os.Stat(a.path)
	if err != nil {
		return "", "", fmt.Errorf("stat cookie file %s: %w", a.path, err)
	}
	if a.loaded && info.ModTime().Equal(a.modTime) && info.Size() == a.size {
		return a.user, a.pass, nil
	}

	raw, err := os.ReadFile(a.path)
	if err != nil {
		return "", "", fmt.Errorf("read cookie file %s: %w", a.path, err)
	}
	user, pass, ok := strings.Cut(strings.TrimSpace(string(raw)), ":")
	if !ok {
		return "", "", fmt.Errorf("cookie file %s is malformed: want user:password", a.path)
	}

	a.user, a.pass = user, pass
	a.modTime, a.size, a.loaded = info.ModTime(), info.Size(), true
	return a.user, a.pass, nil
}

// Chain subdirectories bitcoind uses beneath the data directory. Mainnet keeps
// its cookie at the data directory root.
var chainSubdir = map[string]string{
	"main":     "",
	"mainnet":  "",
	"test":     "testnet3",
	"testnet":  "testnet3",
	"testnet3": "testnet3",
	"testnet4": "testnet4",
	"test4":    "testnet4",
	"signet":   "signet",
	"regtest":  "regtest",
}

// CookiePath returns the conventional cookie location for a data directory and
// chain name.
func CookiePath(dataDir, chain string) string {
	sub, ok := chainSubdir[strings.ToLower(chain)]
	if !ok {
		sub = strings.ToLower(chain)
	}
	return filepath.Join(dataDir, sub, ".cookie")
}

// DefaultDataDirs lists the data directories to probe for a cookie file when
// the user supplied neither credentials nor an explicit path.
func DefaultDataDirs() []string {
	dirs := []string{"/data", "/bitcoin", "/root/.bitcoin"}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append([]string{filepath.Join(home, ".bitcoin")}, dirs...)
	}
	return dirs
}

// ResolveAuth picks an Authenticator from the supplied configuration. An
// explicit user/password wins; otherwise an explicit cookie path is used; with
// neither, the conventional data directories are probed for a cookie file.
func ResolveAuth(user, pass, cookiePath, chain string) (Authenticator, error) {
	if user != "" || pass != "" {
		if user == "" || pass == "" {
			return nil, fmt.Errorf("rpc user and password must be set together")
		}
		return StaticAuth{User: user, Pass: pass}, nil
	}

	if cookiePath != "" {
		auth := NewCookieAuth(cookiePath)
		if _, _, err := auth.Credentials(); err != nil {
			// Not fatal: the node may not have started yet, and the cookie is
			// re-read on every scrape.
			return auth, nil
		}
		return auth, nil
	}

	var probed []string
	for _, dir := range DefaultDataDirs() {
		candidate := CookiePath(dir, chain)
		probed = append(probed, candidate)
		if _, err := os.Stat(candidate); err == nil {
			return NewCookieAuth(candidate), nil
		}
	}
	return nil, fmt.Errorf("no rpc credentials: set -rpc.user/-rpc.password or -rpc.cookie-file (probed %s)", strings.Join(probed, ", "))
}
