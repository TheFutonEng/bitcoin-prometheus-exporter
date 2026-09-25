package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCallDecodesResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if req.Method != "getblockcount" {
			t.Errorf("method = %q, want getblockcount", req.Method)
		}
		// A nil params slice must still serialize as [], which bitcoind requires.
		if req.Params == nil || len(req.Params) != 0 {
			t.Errorf("params = %#v, want empty slice", req.Params)
		}
		_, _ = w.Write([]byte(`{"result":840000,"error":null,"id":"bitcoin-exporter"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil)
	var height int64
	if err := c.Call(context.Background(), "getblockcount", nil, &height); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if height != 840000 {
		t.Fatalf("height = %d, want 840000", height)
	}
}

func TestCallSurfacesRPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// bitcoind answers method-level failures with HTTP 500 and a JSON body.
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"result":null,"error":{"code":-8,"message":"Block height out of range"},"id":"x"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil)
	err := c.Call(context.Background(), "getblockstats", []any{99999999}, nil)
	if err == nil {
		t.Fatal("Call: want error, got nil")
	}
	if got := Code(err); got != ErrInvalidParameter {
		t.Fatalf("Code(err) = %d, want %d", got, ErrInvalidParameter)
	}
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || !strings.Contains(rpcErr.Message, "out of range") {
		t.Fatalf("err = %v, want an *Error carrying the message", err)
	}
}

func TestCodeIgnoresTransportErrors(t *testing.T) {
	// A connection failure is not an RPC error, so collectors must not mistake
	// it for the node answering.
	c := newTestClient(t, "http://127.0.0.1:1", nil)
	err := c.Call(context.Background(), "uptime", nil, nil)
	if err == nil {
		t.Fatal("want a transport error")
	}
	if got := Code(err); got != 0 {
		t.Fatalf("Code(err) = %d, want 0", got)
	}
}

func TestCallWalletUsesWalletEndpoint(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"result":{},"error":null,"id":"x"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil)
	if err := c.CallWallet(context.Background(), "cold storage", "getwalletinfo", nil, nil); err != nil {
		t.Fatalf("CallWallet: %v", err)
	}
	if want := "/wallet/cold storage"; gotPath != want {
		t.Fatalf("path = %q, want %q", gotPath, want)
	}
}

func TestBasicAuthIsSent(t *testing.T) {
	var gotUser, gotPass string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		_, _ = w.Write([]byte(`{"result":1,"error":null,"id":"x"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, StaticAuth{User: "alice", Pass: "s3cret"})
	if err := c.Call(context.Background(), "uptime", nil, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if gotUser != "alice" || gotPass != "s3cret" {
		t.Fatalf("basic auth = %q/%q, want alice/s3cret", gotUser, gotPass)
	}
}

func TestUnauthorizedIsReportedClearly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil)
	err := c.Call(context.Background(), "uptime", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("err = %v, want an unauthorized error", err)
	}
}

func TestCookieAuthRereadsRotatedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".cookie")
	write := func(content string) {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write cookie: %v", err)
		}
	}

	write("__cookie__:first\n")
	auth := NewCookieAuth(path)
	user, pass, err := auth.Credentials()
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if user != "__cookie__" || pass != "first" {
		t.Fatalf("credentials = %q/%q, want __cookie__/first", user, pass)
	}

	// bitcoind rewrites the cookie on every restart.
	write("__cookie__:second")
	// Some filesystems have coarse timestamps; nudge mtime so the change is
	// unambiguous rather than depending on clock resolution.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if _, pass, err = auth.Credentials(); err != nil {
		t.Fatalf("Credentials after rotation: %v", err)
	}
	if pass != "second" {
		t.Fatalf("password = %q, want second", pass)
	}
}

func TestCookieAuthRejectsMalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".cookie")
	if err := os.WriteFile(path, []byte("no-separator"), 0o600); err != nil {
		t.Fatalf("write cookie: %v", err)
	}
	if _, _, err := NewCookieAuth(path).Credentials(); err == nil {
		t.Fatal("want an error for a cookie file without a colon")
	}
}

func TestCookiePath(t *testing.T) {
	for _, tc := range []struct{ chain, want string }{
		{"main", "/data/.cookie"},
		{"regtest", "/data/regtest/.cookie"},
		{"signet", "/data/signet/.cookie"},
		{"testnet3", "/data/testnet3/.cookie"},
		{"testnet4", "/data/testnet4/.cookie"},
	} {
		if got := CookiePath("/data", tc.chain); got != tc.want {
			t.Errorf("CookiePath(/data, %q) = %q, want %q", tc.chain, got, tc.want)
		}
	}
}

func TestResolveAuthPrefersExplicitCredentials(t *testing.T) {
	auth, err := ResolveAuth("bob", "pw", "/nonexistent/.cookie", "main")
	if err != nil {
		t.Fatalf("ResolveAuth: %v", err)
	}
	if _, ok := auth.(StaticAuth); !ok {
		t.Fatalf("auth = %T, want StaticAuth", auth)
	}
}

func TestResolveAuthRejectsHalfCredentials(t *testing.T) {
	if _, err := ResolveAuth("bob", "", "", "main"); err == nil {
		t.Fatal("want an error when only the user is set")
	}
}

func TestNewAddsMissingScheme(t *testing.T) {
	c, err := New(Options{URL: "127.0.0.1:8332"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if want := "http://127.0.0.1:8332"; c.Endpoint() != want {
		t.Fatalf("Endpoint() = %q, want %q", c.Endpoint(), want)
	}
}

func newTestClient(t *testing.T, url string, auth Authenticator) *Client {
	t.Helper()
	c, err := New(Options{URL: url, Auth: auth, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}
