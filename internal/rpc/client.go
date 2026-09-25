// Package rpc implements a minimal Bitcoin Core JSON-RPC client.
package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Error is a JSON-RPC error returned by bitcoind.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("bitcoind rpc error %d: %s", e.Code, e.Message)
}

// Bitcoin Core error codes the collectors care about, from src/rpc/protocol.h.
const (
	ErrMethodNotFound   = -32601
	ErrInvalidParameter = -8
	ErrMiscError        = -1
	ErrWalletNotFound   = -18
	ErrMethodDeprecated = -32
)

// Code returns the JSON-RPC error code carried by err, or 0 if err is not an
// RPC error.
func Code(err error) int {
	var rpcErr *Error
	if errors.As(err, &rpcErr) {
		return rpcErr.Code
	}
	return 0
}

// Observer is notified about every completed RPC call so the exporter can
// publish latency and error metrics for itself.
type Observer interface {
	ObserveRPC(method string, dur time.Duration, err error)
}

// Client talks to a single bitcoind JSON-RPC endpoint.
type Client struct {
	endpoint *url.URL
	auth     Authenticator
	http     *http.Client
	observer Observer
}

// Options configure a Client.
type Options struct {
	// URL is the base RPC endpoint, e.g. http://127.0.0.1:8332.
	URL string
	// Auth supplies the HTTP basic credentials for each request.
	Auth Authenticator
	// Timeout bounds a single RPC call. Zero means 5s.
	Timeout time.Duration
	// Observer, if set, receives per-call timing and error information.
	Observer Observer
}

// New builds a Client from opts.
func New(opts Options) (*Client, error) {
	raw := opts.URL
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse rpc url %q: %w", opts.URL, err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("rpc url %q has no host", opts.URL)
	}
	u.Path = strings.TrimSuffix(u.Path, "/")

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	return &Client{
		endpoint: u,
		auth:     opts.Auth,
		observer: opts.Observer,
		http: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        8,
				MaxIdleConnsPerHost: 8,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}, nil
}

// Endpoint returns the base URL the client was configured with.
func (c *Client) Endpoint() string { return c.endpoint.String() }

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type response struct {
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
}

// Call invokes method with params and unmarshals the result into out, which
// may be nil when the caller does not need the payload.
func (c *Client) Call(ctx context.Context, method string, params []any, out any) error {
	return c.call(ctx, "", method, params, out)
}

// CallWallet is Call against a specific loaded wallet, using bitcoind's
// /wallet/<name> endpoint. An empty wallet name targets the default endpoint.
func (c *Client) CallWallet(ctx context.Context, wallet, method string, params []any, out any) error {
	return c.call(ctx, wallet, method, params, out)
}

func (c *Client) call(ctx context.Context, wallet, method string, params []any, out any) error {
	start := time.Now()
	err := c.do(ctx, wallet, method, params, out)
	if c.observer != nil {
		c.observer.ObserveRPC(method, time.Since(start), err)
	}
	return err
}

func (c *Client) do(ctx context.Context, wallet, method string, params []any, out any) error {
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(request{JSONRPC: "1.0", ID: "bitcoin-exporter", Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("marshal %s request: %w", method, err)
	}

	endpoint := *c.endpoint
	if wallet != "" {
		endpoint.Path = endpoint.Path + "/wallet/" + wallet
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.auth != nil {
		user, pass, err := c.auth.Credentials()
		if err != nil {
			return fmt.Errorf("rpc credentials: %w", err)
		}
		req.SetBasicAuth(user, pass)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	// bitcoind answers with 500 and a JSON-RPC error body for most method
	// failures, so only treat non-JSON statuses as transport errors.
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("%s: read response: %w", method, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%s: unauthorized (check rpc credentials)", method)
	}

	var rpcResp response
	if err := json.Unmarshal(payload, &rpcResp); err != nil {
		return fmt.Errorf("%s: http %d: %s", method, resp.StatusCode, strings.TrimSpace(truncate(string(payload), 200)))
	}
	if rpcResp.Error != nil {
		return rpcResp.Error
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: unexpected http %d", method, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(rpcResp.Result, out); err != nil {
		return fmt.Errorf("%s: decode result: %w", method, err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// SetObserver attaches an Observer after construction, which lets the caller
// build an observer that itself needs the client.
func (c *Client) SetObserver(o Observer) { c.observer = o }
