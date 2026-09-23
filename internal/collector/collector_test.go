package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TheFutonEng/bitcoin-prometheus-exporter/internal/rpc"
)

// fakeNode answers JSON-RPC calls from a canned table. A value is either a raw
// JSON result or an *rpc.Error to be returned as a JSON-RPC failure.
type fakeNode struct {
	t         *testing.T
	responses map[string]any
	srv       *httptest.Server

	// Collectors scrape concurrently, so several handler goroutines record
	// calls at once.
	mu    sync.Mutex
	calls []string
}

func newFakeNode(t *testing.T, responses map[string]any) *fakeNode {
	t.Helper()
	n := &fakeNode{t: t, responses: responses}
	n.srv = httptest.NewServer(http.HandlerFunc(n.serve))
	t.Cleanup(n.srv.Close)
	return n
}

func (n *fakeNode) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		n.t.Errorf("decode request: %v", err)
		return
	}
	n.mu.Lock()
	n.calls = append(n.calls, req.Method)
	n.mu.Unlock()

	resp, ok := n.responses[req.Method]
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"result":null,"error":{"code":%d,"message":"Method not found"},"id":"x"}`, rpc.ErrMethodNotFound)
		return
	}
	if rpcErr, isErr := resp.(*rpc.Error); isErr {
		w.WriteHeader(http.StatusInternalServerError)
		out, _ := json.Marshal(map[string]any{"result": nil, "error": rpcErr, "id": "x"})
		_, _ = w.Write(out)
		return
	}
	fmt.Fprintf(w, `{"result":%s,"error":null,"id":"x"}`, resp)
}

// methodCalls returns how many times the node was asked for a method.
func (n *fakeNode) methodCalls(method string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	count := 0
	for _, called := range n.calls {
		if called == method {
			count++
		}
	}
	return count
}

func (n *fakeNode) client() *rpc.Client {
	n.t.Helper()
	c, err := rpc.New(rpc.Options{URL: n.srv.URL, Timeout: 2 * time.Second})
	if err != nil {
		n.t.Fatalf("rpc.New: %v", err)
	}
	return c
}

// updater adapts a Collector to prometheus.Collector so testutil can drive it.
type updater struct {
	t *testing.T
	c Collector
}

func (u updater) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range allDescs {
		ch <- d
	}
}

func (u updater) Collect(ch chan<- prometheus.Metric) {
	if err := u.c.Update(context.Background(), ch); err != nil {
		u.t.Logf("Update returned: %v", err)
	}
}

func build(t *testing.T, name string, node *fakeNode, cfg Config) prometheus.Collector {
	t.Helper()
	reg, ok := registry[name]
	if !ok {
		t.Fatalf("collector %q is not registered", name)
	}
	c, err := reg.factory(node.client(), cfg)
	if err != nil {
		t.Fatalf("build %s: %v", name, err)
	}
	return updater{t: t, c: c}
}

func expectMetrics(t *testing.T, c prometheus.Collector, want string, names ...string) {
	t.Helper()
	if err := testutil.CollectAndCompare(c, strings.NewReader(want), names...); err != nil {
		t.Errorf("unexpected metrics:\n%v", err)
	}
}

const blockchainInfoJSON = `{
  "chain":"main","blocks":840000,"headers":840002,
  "bestblockhash":"0000000000000000000320283a032748cef8227873ff4872689bf23f1cda83a5",
  "difficulty":86388558925171.02,"time":1713571767,"mediantime":1713568316,
  "verificationprogress":0.9999,"initialblockdownload":false,
  "chainwork":"0000000000000000000000000000000000000000753bdab0e0d745453677442b",
  "size_on_disk":648000000000,"pruned":false,"warnings":[]
}`

func TestChainCollector(t *testing.T) {
	node := newFakeNode(t, map[string]any{"getblockchaininfo": blockchainInfoJSON})

	expectMetrics(t, build(t, "chain", node, Config{}), `
# HELP bitcoin_blocks Height of the most-work fully validated chain.
# TYPE bitcoin_blocks gauge
bitcoin_blocks 840000
# HELP bitcoin_headers Height of the best known block header.
# TYPE bitcoin_headers gauge
bitcoin_headers 840002
# HELP bitcoin_blocks_behind Known headers minus validated blocks; 0 when the node is at the tip.
# TYPE bitcoin_blocks_behind gauge
bitcoin_blocks_behind 2
# HELP bitcoin_chain_info Always 1, labelled with the chain the node is running on.
# TYPE bitcoin_chain_info gauge
bitcoin_chain_info{chain="main"} 1
# HELP bitcoin_initial_block_download 1 while the node considers itself in initial block download.
# TYPE bitcoin_initial_block_download gauge
bitcoin_initial_block_download 0
# HELP bitcoin_size_on_disk_bytes Estimated size of the block and undo files on disk.
# TYPE bitcoin_size_on_disk_bytes gauge
bitcoin_size_on_disk_bytes 6.48e+11
# HELP bitcoin_warnings Number of warnings the node is currently reporting.
# TYPE bitcoin_warnings gauge
bitcoin_warnings 0
`,
		"bitcoin_blocks", "bitcoin_headers", "bitcoin_blocks_behind", "bitcoin_chain_info",
		"bitcoin_initial_block_download", "bitcoin_size_on_disk_bytes", "bitcoin_warnings")
}

func TestChainCollectorOmitsFieldsTheNodeDidNotSend(t *testing.T) {
	// An older node answers without time, prune or automatic_pruning fields;
	// reporting them as 0 would be a lie rather than an absence.
	node := newFakeNode(t, map[string]any{"getblockchaininfo": `{
	  "chain":"test","blocks":10,"headers":10,"difficulty":1,"verificationprogress":1,
	  "initialblockdownload":false,"chainwork":"01","size_on_disk":100,"pruned":false,"warnings":""
	}`})

	got := testutil.CollectAndCount(build(t, "chain", node, Config{}), "bitcoin_best_block_time_seconds", "bitcoin_prune_target_size_bytes")
	if got != 0 {
		t.Fatalf("optional metric count = %d, want 0", got)
	}
}

func TestChainCollectorAcceptsBothWarningShapes(t *testing.T) {
	for name, warnings := range map[string]string{
		"legacy string": `"Unknown new rules activated"`,
		"modern array":  `["Unknown new rules activated"]`,
	} {
		t.Run(name, func(t *testing.T) {
			node := newFakeNode(t, map[string]any{"getblockchaininfo": fmt.Sprintf(`{
			  "chain":"main","blocks":1,"headers":1,"difficulty":1,"verificationprogress":1,
			  "initialblockdownload":false,"chainwork":"01","size_on_disk":1,"pruned":false,
			  "warnings":%s
			}`, warnings)})

			expectMetrics(t, build(t, "chain", node, Config{}), `
# HELP bitcoin_warning_info Always 1, labelled with a warning the node is currently reporting.
# TYPE bitcoin_warning_info gauge
bitcoin_warning_info{warning="Unknown new rules activated"} 1
# HELP bitcoin_warnings Number of warnings the node is currently reporting.
# TYPE bitcoin_warnings gauge
bitcoin_warnings 1
`, "bitcoin_warnings", "bitcoin_warning_info")
		})
	}
}

func TestMempoolCollector(t *testing.T) {
	node := newFakeNode(t, map[string]any{"getmempoolinfo": `{
	  "loaded":true,"size":12345,"bytes":6000000,"usage":30000000,"total_fee":0.5,
	  "maxmempool":300000000,"mempoolminfee":0.00001,"minrelaytxfee":0.00001,
	  "incrementalrelayfee":0.00001,"unbroadcastcount":0,"fullrbf":true
	}`})

	expectMetrics(t, build(t, "mempool", node, Config{}), `
# HELP bitcoin_mempool_txs Number of transactions currently in the mempool.
# TYPE bitcoin_mempool_txs gauge
bitcoin_mempool_txs 12345
# HELP bitcoin_mempool_fill_ratio Mempool memory usage divided by -maxmempool, 0 to 1.
# TYPE bitcoin_mempool_fill_ratio gauge
bitcoin_mempool_fill_ratio 0.1
# HELP bitcoin_mempool_min_fee_sat_per_vbyte Lowest fee rate the mempool currently accepts, in sat/vB.
# TYPE bitcoin_mempool_min_fee_sat_per_vbyte gauge
bitcoin_mempool_min_fee_sat_per_vbyte 1
`, "bitcoin_mempool_txs", "bitcoin_mempool_fill_ratio", "bitcoin_mempool_min_fee_sat_per_vbyte")
}

func TestFeeCollectorReportsUnavailableEstimates(t *testing.T) {
	// This is the normal answer on regtest and on a node that has not yet
	// observed enough confirmations.
	node := newFakeNode(t, map[string]any{
		"estimatesmartfee": `{"errors":["Insufficient data or no feerate found"],"blocks":6}`,
	})

	expectMetrics(t, build(t, "fees", node, Config{FeeTargets: []int{6}, FeeMode: "conservative"}), `
# HELP bitcoin_fee_estimate_available 1 if the node had enough data to answer estimatesmartfee for this target.
# TYPE bitcoin_fee_estimate_available gauge
bitcoin_fee_estimate_available{blocks="6",mode="conservative"} 0
`, "bitcoin_fee_estimate_available", "bitcoin_fee_estimate_btc_per_kvb")
}

func TestFeeCollectorConvertsToSatPerVByte(t *testing.T) {
	node := newFakeNode(t, map[string]any{
		"estimatesmartfee": `{"feerate":0.00015000,"blocks":6}`,
	})

	expectMetrics(t, build(t, "fees", node, Config{FeeTargets: []int{6}, FeeMode: "economical"}), `
# HELP bitcoin_fee_estimate_btc_per_kvb Estimated fee rate to confirm within N blocks, in BTC/kvB.
# TYPE bitcoin_fee_estimate_btc_per_kvb gauge
bitcoin_fee_estimate_btc_per_kvb{blocks="6",mode="economical"} 0.00015
# HELP bitcoin_fee_estimate_sat_per_vbyte Estimated fee rate to confirm within N blocks, in sat/vB.
# TYPE bitcoin_fee_estimate_sat_per_vbyte gauge
bitcoin_fee_estimate_sat_per_vbyte{blocks="6",mode="economical"} 15
`, "bitcoin_fee_estimate_btc_per_kvb", "bitcoin_fee_estimate_sat_per_vbyte")
}

func TestFeeCollectorRejectsOutOfRangeTargets(t *testing.T) {
	node := newFakeNode(t, map[string]any{})
	if _, err := registry["fees"].factory(node.client(), Config{FeeTargets: []int{0}}); err == nil {
		t.Fatal("want an error for a confirmation target below 1")
	}
}

func TestMiningCollectorWithBlockStats(t *testing.T) {
	node := newFakeNode(t, map[string]any{
		"getmininginfo": `{"blocks":840000,"difficulty":86388558925171.02,"networkhashps":6.2e20,"pooledtx":12345}`,
		"getblockstats": `{
		  "height":840000,"time":1713571767,"total_size":1600000,"total_weight":3993000,
		  "txs":3050,"ins":8000,"outs":9000,"total_out":1200000000000,"totalfee":30000000,
		  "subsidy":312500000,"avgfeerate":25,"minfeerate":3,"maxfeerate":500,
		  "avgtxsize":520,"mediantxsize":300,"swtxs":2800,"swtotal_weight":3000000,
		  "utxo_increase":1000,"feerate_percentiles":[4,8,20,40,90]
		}`,
	})

	expectMetrics(t, build(t, "mining", node, Config{BlockStats: true}), `
# HELP bitcoin_network_hashps Estimated network hash rate in hashes per second.
# TYPE bitcoin_network_hashps gauge
bitcoin_network_hashps 6.2e+20
# HELP bitcoin_latest_block_stats_available 1 if getblockstats succeeded for the chain tip during this scrape.
# TYPE bitcoin_latest_block_stats_available gauge
bitcoin_latest_block_stats_available 1
# HELP bitcoin_latest_block_feerate_sat_per_vbyte Fee rate distribution within the block at the tip, in sat/vB.
# TYPE bitcoin_latest_block_feerate_sat_per_vbyte gauge
bitcoin_latest_block_feerate_sat_per_vbyte{stat="avg"} 25
bitcoin_latest_block_feerate_sat_per_vbyte{stat="max"} 500
bitcoin_latest_block_feerate_sat_per_vbyte{stat="min"} 3
bitcoin_latest_block_feerate_sat_per_vbyte{stat="p10"} 4
bitcoin_latest_block_feerate_sat_per_vbyte{stat="p25"} 8
bitcoin_latest_block_feerate_sat_per_vbyte{stat="p50"} 20
bitcoin_latest_block_feerate_sat_per_vbyte{stat="p75"} 40
bitcoin_latest_block_feerate_sat_per_vbyte{stat="p90"} 90
`, "bitcoin_network_hashps", "bitcoin_latest_block_stats_available", "bitcoin_latest_block_feerate_sat_per_vbyte")
}

func TestMiningCollectorToleratesPrunedBlockStats(t *testing.T) {
	// A pruned node refuses getblockstats for blocks whose undo data is gone.
	node := newFakeNode(t, map[string]any{
		"getmininginfo": `{"blocks":840000,"difficulty":1,"networkhashps":1,"pooledtx":0}`,
		"getblockstats": &rpc.Error{Code: rpc.ErrMiscError, Message: "Block not available (pruned data)"},
	})

	c := build(t, "mining", node, Config{BlockStats: true})
	expectMetrics(t, c, `
# HELP bitcoin_latest_block_stats_available 1 if getblockstats succeeded for the chain tip during this scrape.
# TYPE bitcoin_latest_block_stats_available gauge
bitcoin_latest_block_stats_available 0
`, "bitcoin_latest_block_stats_available")

	if err := c.(updater).c.Update(context.Background(), make(chan prometheus.Metric, 64)); err != nil {
		t.Fatalf("a refused getblockstats must not fail the collector, got: %v", err)
	}
}

func TestPeerCollectorAggregatesAndDetails(t *testing.T) {
	peers := `[
	  {"id":0,"addr":"203.0.113.1:8333","network":"ipv4","version":70016,"subver":"/Satoshi:27.0.0/",
	   "inbound":false,"connection_type":"outbound-full-relay","transport_protocol_type":"v2",
	   "pingtime":0.05,"minping":0.04,"bytessent":100,"bytesrecv":200,"conntime":1713000000,
	   "lastsend":1713000100,"lastrecv":1713000101,"relaytxes":true,"bip152_hb_to":true,
	   "minfeefilter":0.00001,"synced_headers":840000,"synced_blocks":840000},
	  {"id":1,"addr":"[2001:db8::1]:8333","network":"ipv6","version":70016,"subver":"/Satoshi:27.0.0/",
	   "inbound":true,"connection_type":"inbound","transport_protocol_type":"v1",
	   "pingtime":0.15,"minping":0.10,"bytessent":10,"bytesrecv":20,"conntime":1713000050,
	   "lastsend":1713000100,"lastrecv":1713000101,"relaytxes":false,"bip152_hb_from":true}
	]`
	node := newFakeNode(t, map[string]any{"getpeerinfo": peers, "listbanned": `[{"address":"198.51.100.0/24","banned_until":1713999999}]`})

	expectMetrics(t, build(t, "peers", node, Config{}), `
# HELP bitcoin_peers_by_network Connected peers grouped by the network they are reachable on.
# TYPE bitcoin_peers_by_network gauge
bitcoin_peers_by_network{network="ipv4"} 1
bitcoin_peers_by_network{network="ipv6"} 1
# HELP bitcoin_peers_by_subversion Connected peers grouped by their advertised user agent.
# TYPE bitcoin_peers_by_subversion gauge
bitcoin_peers_by_subversion{subversion="/Satoshi:27.0.0/"} 2
# HELP bitcoin_peers_ping_seconds Aggregate round-trip time across connected peers.
# TYPE bitcoin_peers_ping_seconds gauge
bitcoin_peers_ping_seconds{stat="avg"} 0.1
bitcoin_peers_ping_seconds{stat="max"} 0.15
bitcoin_peers_ping_seconds{stat="min"} 0.05
# HELP bitcoin_peers_relaying_txs Connected peers the node relays transactions to.
# TYPE bitcoin_peers_relaying_txs gauge
bitcoin_peers_relaying_txs 1
# HELP bitcoin_peers_high_bandwidth Peers in a BIP152 high-bandwidth compact block relationship, by direction.
# TYPE bitcoin_peers_high_bandwidth gauge
bitcoin_peers_high_bandwidth{direction="from"} 1
bitcoin_peers_high_bandwidth{direction="to"} 1
# HELP bitcoin_peers_banned Number of entries in the node's ban list.
# TYPE bitcoin_peers_banned gauge
bitcoin_peers_banned 1
`,
		"bitcoin_peers_by_network", "bitcoin_peers_by_subversion", "bitcoin_peers_ping_seconds",
		"bitcoin_peers_relaying_txs", "bitcoin_peers_high_bandwidth", "bitcoin_peers_banned")

	// Per-peer series stay off until explicitly asked for.
	if n := testutil.CollectAndCount(build(t, "peers", node, Config{}), "bitcoin_peer_info"); n != 0 {
		t.Fatalf("bitcoin_peer_info count = %d without -collector.peers.detail, want 0", n)
	}
	if n := testutil.CollectAndCount(build(t, "peers", node, Config{PeerDetail: true}), "bitcoin_peer_info"); n != 2 {
		t.Fatalf("bitcoin_peer_info count = %d with detail enabled, want 2", n)
	}
}

func TestWalletCollectorHandlesDisabledWallet(t *testing.T) {
	// -disablewallet removes the wallet RPCs entirely.
	node := newFakeNode(t, map[string]any{
		"listwallets": &rpc.Error{Code: rpc.ErrMethodNotFound, Message: "Method not found"},
	})

	c := build(t, "wallet", node, Config{})
	expectMetrics(t, c, `
# HELP bitcoin_wallets_loaded Number of wallets currently loaded by the node.
# TYPE bitcoin_wallets_loaded gauge
bitcoin_wallets_loaded 0
`, "bitcoin_wallets_loaded")

	if err := c.(updater).c.Update(context.Background(), make(chan prometheus.Metric, 16)); err != nil {
		t.Fatalf("a node without wallet support must not fail the collector, got: %v", err)
	}
}

func TestWalletCollectorReportsRescanProgress(t *testing.T) {
	node := newFakeNode(t, map[string]any{
		"listwallets":   `["hot"]`,
		"getwalletinfo": `{"walletname":"hot","walletversion":169900,"format":"sqlite","txcount":42,"paytxfee":0,"scanning":{"duration":120,"progress":0.25},"descriptors":true}`,
		"getbalances":   `{"mine":{"trusted":1.5,"untrusted_pending":0.25,"immature":0}}`,
	})

	expectMetrics(t, build(t, "wallet", node, Config{}), `
# HELP bitcoin_wallet_scanning 1 while the wallet is rescanning the chain.
# TYPE bitcoin_wallet_scanning gauge
bitcoin_wallet_scanning{wallet="hot"} 1
# HELP bitcoin_wallet_scanning_progress Progress of the in-progress rescan, 0 to 1.
# TYPE bitcoin_wallet_scanning_progress gauge
bitcoin_wallet_scanning_progress{wallet="hot"} 0.25
# HELP bitcoin_wallet_balance_btc Wallet balance in BTC, split by ownership and spendability.
# TYPE bitcoin_wallet_balance_btc gauge
bitcoin_wallet_balance_btc{category="immature",owner="mine",wallet="hot"} 0
bitcoin_wallet_balance_btc{category="trusted",owner="mine",wallet="hot"} 1.5
bitcoin_wallet_balance_btc{category="untrusted_pending",owner="mine",wallet="hot"} 0.25
`, "bitcoin_wallet_scanning", "bitcoin_wallet_scanning_progress", "bitcoin_wallet_balance_btc")
}

func TestWalletCollectorHonoursNameFilter(t *testing.T) {
	node := newFakeNode(t, map[string]any{
		"listwallets":   `["hot","cold"]`,
		"getwalletinfo": `{"walletname":"hot","walletversion":1,"format":"sqlite","txcount":1,"paytxfee":0,"scanning":false}`,
		"getbalances":   `{"mine":{"trusted":1}}`,
	})

	c := build(t, "wallet", node, Config{Wallets: []string{"hot", "nonexistent"}})
	if n := testutil.CollectAndCount(c, "bitcoin_wallet_txs"); n != 1 {
		t.Fatalf("wallet series = %d, want 1 (only the named, loaded wallet)", n)
	}
	if n := node.methodCalls("getwalletinfo"); n != 1 {
		t.Fatalf("getwalletinfo calls = %d, want 1 (the filtered wallets must not be queried)", n)
	}
}

func TestExporterReportsNodeDown(t *testing.T) {
	client, err := rpc.New(rpc.Options{URL: "http://127.0.0.1:1", Timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("rpc.New: %v", err)
	}
	e, err := NewExporter(client, []string{"chain"}, Config{Logger: discardLogger()}, time.Second)
	if err != nil {
		t.Fatalf("NewExporter: %v", err)
	}

	expectMetrics(t, e, `
# HELP bitcoin_up 1 if the node answered RPC during this scrape, 0 otherwise.
# TYPE bitcoin_up gauge
bitcoin_up 0
# HELP bitcoin_exporter_collector_success 1 if the collector completed without error during the last scrape.
# TYPE bitcoin_exporter_collector_success gauge
bitcoin_exporter_collector_success{collector="chain"} 0
`, "bitcoin_up", "bitcoin_exporter_collector_success")
}

func TestExporterKeepsGoingWhenOneCollectorFails(t *testing.T) {
	// getmempoolinfo is missing from the table, so the mempool collector fails
	// while the chain collector still reports.
	node := newFakeNode(t, map[string]any{
		"uptime":            `3600`,
		"getblockchaininfo": blockchainInfoJSON,
	})
	e, err := NewExporter(node.client(), []string{"chain", "mempool"}, Config{Logger: discardLogger()}, 2*time.Second)
	if err != nil {
		t.Fatalf("NewExporter: %v", err)
	}

	expectMetrics(t, e, `
# HELP bitcoin_up 1 if the node answered RPC during this scrape, 0 otherwise.
# TYPE bitcoin_up gauge
bitcoin_up 1
# HELP bitcoin_exporter_collector_success 1 if the collector completed without error during the last scrape.
# TYPE bitcoin_exporter_collector_success gauge
bitcoin_exporter_collector_success{collector="chain"} 1
bitcoin_exporter_collector_success{collector="mempool"} 0
# HELP bitcoin_blocks Height of the most-work fully validated chain.
# TYPE bitcoin_blocks gauge
bitcoin_blocks 840000
`, "bitcoin_up", "bitcoin_exporter_collector_success", "bitcoin_blocks")
}

func TestExporterRejectsUnknownCollector(t *testing.T) {
	node := newFakeNode(t, map[string]any{})
	if _, err := NewExporter(node.client(), []string{"nope"}, Config{}, time.Second); err == nil {
		t.Fatal("want an error for an unknown collector name")
	}
}

func TestExporterRegistersWithAPedanticRegistry(t *testing.T) {
	// The pedantic registry rejects any metric whose descriptor Describe did
	// not announce, which is the check that keeps allDescs honest.
	node := newFakeNode(t, map[string]any{
		"uptime":            `1`,
		"getblockchaininfo": blockchainInfoJSON,
		"getmempoolinfo":    `{"loaded":true,"size":1,"bytes":1,"usage":1,"total_fee":0,"maxmempool":1,"mempoolminfee":0,"minrelaytxfee":0,"unbroadcastcount":0}`,
		"getnetworkinfo":    `{"version":270000,"subversion":"/Satoshi:27.0.0/","protocolversion":70016,"localrelay":true,"timeoffset":0,"connections":10,"connections_in":2,"connections_out":8,"networkactive":true,"relayfee":0.00001,"incrementalfee":0.00001,"networks":[]}`,
		"getnettotals":      `{"totalbytesrecv":1,"totalbytessent":2,"uploadtarget":{}}`,
		"getmininginfo":     `{"blocks":840000,"difficulty":1,"networkhashps":1,"pooledtx":0}`,
		"getblockstats":     `{"height":840000,"time":1,"total_size":1,"total_weight":1,"txs":1,"ins":1,"outs":1,"total_out":1,"totalfee":1,"subsidy":1,"avgfeerate":1,"minfeerate":1,"maxfeerate":1,"avgtxsize":1,"mediantxsize":1,"swtxs":1,"swtotal_weight":1,"utxo_increase":1,"feerate_percentiles":[1,2,3,4,5]}`,
		"estimatesmartfee":  `{"feerate":0.0001,"blocks":6}`,
		"getpeerinfo":       `[]`,
		"listbanned":        `[]`,
		"listwallets":       `[]`,
	})

	e, err := NewExporter(node.client(), Registered(), Config{BlockStats: true, Logger: discardLogger()}, 5*time.Second)
	if err != nil {
		t.Fatalf("NewExporter: %v", err)
	}

	reg := prometheus.NewPedanticRegistry()
	if err := reg.Register(e); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := reg.Gather(); err != nil {
		t.Fatalf("Gather: %v", err)
	}
}

func TestRegisteredDefaults(t *testing.T) {
	want := map[string]bool{
		"chain": true, "network": true, "mempool": true,
		"fees": true, "mining": true, "peers": true, "wallet": false,
	}
	got := Registered()
	if len(got) != len(want) {
		t.Fatalf("Registered() = %v, want %d collectors", got, len(want))
	}
	for _, name := range got {
		enabled, known := want[name]
		if !known {
			t.Errorf("unexpected collector %q", name)
			continue
		}
		if DefaultEnabled(name) != enabled {
			t.Errorf("DefaultEnabled(%q) = %v, want %v", name, DefaultEnabled(name), enabled)
		}
		if Help(name) == "" {
			t.Errorf("collector %q has no help text", name)
		}
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
