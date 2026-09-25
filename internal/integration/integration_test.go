//go:build integration

// Package integration exercises the exporter against a real Bitcoin Core node
// running in a container. Run it with:
//
//	go test -tags integration ./internal/integration/
package integration

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	"github.com/TheFutonEng/bitcoin-prometheus-exporter/internal/collector"
	"github.com/TheFutonEng/bitcoin-prometheus-exporter/internal/rpc"
)

// nodeImage is the node build the exporter is verified against. Override it to
// test another release: NODE_IMAGE=ghcr.io/thefutoneng/bitcoin:31.1 go test ...
var nodeImage = envOr("NODE_IMAGE", "ghcr.io/thefutoneng/bitcoin:31.1-1")

func TestExporterAgainstRealNode(t *testing.T) {
	node := startNode(t)
	node.mine(t, 101)
	node.spend(t, 3)
	node.mine(t, 1)

	client, err := rpc.New(rpc.Options{
		URL:     node.rpcURL,
		Auth:    rpc.NewCookieAuth(node.cookiePath),
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("rpc.New: %v", err)
	}

	exporter, err := collector.NewExporter(client, collector.Registered(), collector.Config{
		BlockStats: true,
		PeerDetail: true,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, 20*time.Second)
	if err != nil {
		t.Fatalf("NewExporter: %v", err)
	}
	client.SetObserver(exporter)

	// The pedantic registry is the same check the binary applies at runtime.
	reg := prometheus.NewPedanticRegistry()
	if err := reg.Register(exporter); err != nil {
		t.Fatalf("Register: %v", err)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	values := flatten(families)

	// Every collector must have completed against a healthy node.
	for _, name := range collector.Registered() {
		key := fmt.Sprintf(`bitcoin_exporter_collector_success{collector="%s"}`, name)
		if got, ok := values[key]; !ok || got != 1 {
			t.Errorf("%s = %v (present: %v), want 1", key, got, ok)
		}
	}

	if got := values["bitcoin_up"]; got != 1 {
		t.Errorf("bitcoin_up = %v, want 1", got)
	}
	if got := values["bitcoin_blocks"]; got != 102 {
		t.Errorf("bitcoin_blocks = %v, want 102", got)
	}
	if got := values["bitcoin_blocks_behind"]; got != 0 {
		t.Errorf("bitcoin_blocks_behind = %v, want 0", got)
	}
	if got := values[`bitcoin_chain_info{chain="regtest"}`]; got != 1 {
		t.Errorf("bitcoin_chain_info{chain=regtest} = %v, want 1", got)
	}
	if got := values["bitcoin_initial_block_download"]; got != 0 {
		t.Errorf("bitcoin_initial_block_download = %v, want 0", got)
	}
	if got := values["bitcoin_latest_block_stats_available"]; got != 1 {
		t.Errorf("bitcoin_latest_block_stats_available = %v, want 1", got)
	}
	if got := values["bitcoin_latest_block_txs"]; got < 2 {
		t.Errorf("bitcoin_latest_block_txs = %v, want the coinbase plus the spends", got)
	}
	if got := values["bitcoin_latest_block_subsidy_sat"]; got != 5e9 {
		t.Errorf("bitcoin_latest_block_subsidy_sat = %v, want 5e9 for an early regtest block", got)
	}
	if got := values["bitcoin_wallets_loaded"]; got != 1 {
		t.Errorf("bitcoin_wallets_loaded = %v, want 1", got)
	}
	if got := values[`bitcoin_wallet_balance_btc{category="trusted",owner="mine",wallet="miner"}`]; got <= 0 {
		t.Errorf("miner wallet trusted balance = %v, want a positive balance", got)
	}
	if got := values["bitcoin_size_on_disk_bytes"]; got <= 0 {
		t.Errorf("bitcoin_size_on_disk_bytes = %v, want a positive size", got)
	}
	if got := values["bitcoin_mempool_loaded"]; got != 1 {
		t.Errorf("bitcoin_mempool_loaded = %v, want 1", got)
	}

	// Fee estimation has no history on a fresh regtest chain, and must be
	// reported as unavailable rather than as a zero fee rate.
	if got, ok := values[`bitcoin_fee_estimate_available{blocks="6",mode="conservative"}`]; !ok || got != 0 {
		t.Errorf("fee estimate availability = %v (present: %v), want 0", got, ok)
	}
	if _, ok := values[`bitcoin_fee_estimate_btc_per_kvb{blocks="6",mode="conservative"}`]; ok {
		t.Error("an unavailable fee estimate must not publish a fee rate")
	}

	if n := testutil.CollectAndCount(exporter, "bitcoin_up"); n != 1 {
		t.Errorf("bitcoin_up series = %d, want 1", n)
	}
}

func TestPeerMetricsAgainstRealNodes(t *testing.T) {
	first := startNode(t)
	second := startNode(t, "-connect="+first.name+":18444")

	client, err := rpc.New(rpc.Options{URL: first.rpcURL, Auth: rpc.NewCookieAuth(first.cookiePath), Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("rpc.New: %v", err)
	}
	exporter, err := collector.NewExporter(client, []string{"peers", "network"}, collector.Config{
		PeerDetail: true,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, 20*time.Second)
	if err != nil {
		t.Fatalf("NewExporter: %v", err)
	}

	// Give the second node a moment to complete its handshake.
	var values map[string]float64
	for range 30 {
		reg := prometheus.NewPedanticRegistry()
		if err := reg.Register(exporter); err != nil {
			t.Fatalf("Register: %v", err)
		}
		families, err := reg.Gather()
		if err != nil {
			t.Fatalf("Gather: %v", err)
		}
		values = flatten(families)
		if values["bitcoin_connections"] > 0 {
			break
		}
		time.Sleep(time.Second)
	}

	if got := values["bitcoin_connections"]; got != 1 {
		t.Fatalf("bitcoin_connections = %v, want 1", got)
	}
	if got := values[`bitcoin_peers{direction="inbound"}`]; got != 1 {
		t.Errorf("inbound peers = %v, want 1", got)
	}
	if got := values[`bitcoin_peers_by_subversion{subversion="/Satoshi:31.1.0/"}`]; got != 1 {
		t.Errorf("peers by subversion = %v, want 1", got)
	}
	if _, ok := values[`bitcoin_peers_ping_seconds{stat="avg"}`]; !ok {
		t.Error("want an aggregate ping metric once a peer is connected")
	}
	_ = second
}

type node struct {
	name       string
	rpcURL     string
	cookiePath string
}

// startNode launches a regtest node in a container and tears it down with the
// test. Each node joins a shared docker network so nodes can reach each other.
func startNode(t *testing.T, extraArgs ...string) *node {
	t.Helper()
	requireDocker(t)

	network := "bitcoin-exporter-it"
	_ = exec.Command("docker", "network", "create", network).Run()

	name := fmt.Sprintf("btc-it-%d-%d", os.Getpid(), time.Now().UnixNano()%1e6)
	dir := t.TempDir()
	// The container writes into the bind mount, so it runs as the test user
	// rather than the image's own uid.
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("chmod tempdir: %v", err)
	}

	// The entrypoint is overridden and every flag passed explicitly so the
	// harness does not depend on how a given image splits entrypoint from cmd.
	// 31.1 kept -datadir in cmd, which our own args would have replaced; 31.1-1
	// moved it into the entrypoint, where repeating it would duplicate it. This
	// way any bitcoind image works, which is what NODE_IMAGE is for. The
	// image's own defaults are covered by the compose stack instead.
	args := []string{
		"run", "-d", "--name", name, "--network", network,
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"-p", "127.0.0.1:0:8332",
		"-v", dir + ":/data",
		"--entrypoint", "/usr/local/bin/bitcoind",
		nodeImage,
		"-datadir=/data", "-printtoconsole", "-chain=regtest", "-server=1",
		"-rpcbind=0.0.0.0", "-rpcallowip=0.0.0.0/0", "-rpcport=8332", "-fallbackfee=0.0002",
	}
	args = append(args, extraArgs...)

	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
	})

	port, err := exec.Command("docker", "port", name, "8332/tcp").Output()
	if err != nil {
		t.Fatalf("docker port: %v", err)
	}
	hostPort := strings.TrimSpace(strings.SplitN(string(port), "\n", 2)[0])

	n := &node{
		name:       name,
		rpcURL:     "http://" + hostPort,
		cookiePath: filepath.Join(dir, "regtest", ".cookie"),
	}

	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if err := n.cli("getblockchaininfo").Run(); err == nil {
			n.cliMust(t, "createwallet", "miner")
			return n
		}
		time.Sleep(500 * time.Millisecond)
	}
	logs, _ := exec.Command("docker", "logs", "--tail", "40", name).CombinedOutput()
	t.Fatalf("node %s never became ready\n%s", name, logs)
	return nil
}

func (n *node) cli(args ...string) *exec.Cmd {
	base := []string{"exec", n.name, "/usr/local/bin/bitcoin-cli", "-datadir=/data", "-chain=regtest", "-rpcport=8332"}
	return exec.Command("docker", append(base, args...)...)
}

func (n *node) cliMust(t *testing.T, args ...string) string {
	t.Helper()
	out, err := n.cli(args...).CombinedOutput()
	if err != nil {
		t.Fatalf("bitcoin-cli %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (n *node) mine(t *testing.T, blocks int) {
	t.Helper()
	addr := n.cliMust(t, "-rpcwallet=miner", "getnewaddress")
	n.cliMust(t, "generatetoaddress", fmt.Sprint(blocks), addr)
}

func (n *node) spend(t *testing.T, count int) {
	t.Helper()
	for range count {
		addr := n.cliMust(t, "-rpcwallet=miner", "getnewaddress")
		n.cliMust(t, "-rpcwallet=miner", "sendtoaddress", addr, "0.5")
	}
}

func requireDocker(t *testing.T) {
	t.Helper()
	if err := exec.CommandContext(context.Background(), "docker", "info").Run(); err != nil {
		t.Skipf("docker is not available: %v", err)
	}
}

// flatten renders gathered families as a map from a metric's exposition-style
// identity to its value, which keeps the assertions readable.
func flatten(families []*dto.MetricFamily) map[string]float64 {
	out := map[string]float64{}
	for _, mf := range families {
		for _, m := range mf.GetMetric() {
			var labels []string
			for _, l := range m.GetLabel() {
				labels = append(labels, fmt.Sprintf("%s=%q", l.GetName(), l.GetValue()))
			}
			key := mf.GetName()
			if len(labels) > 0 {
				key += "{" + strings.Join(labels, ",") + "}"
			}
			switch {
			case m.Gauge != nil:
				out[key] = m.Gauge.GetValue()
			case m.Counter != nil:
				out[key] = m.Counter.GetValue()
			case m.Untyped != nil:
				out[key] = m.Untyped.GetValue()
			}
		}
	}
	return out
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
