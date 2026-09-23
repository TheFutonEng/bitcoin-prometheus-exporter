// Command bitcoin-exporter serves Prometheus metrics scraped from a Bitcoin
// Core node's JSON-RPC interface.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/TheFutonEng/bitcoin-prometheus-exporter/internal/collector"
	"github.com/TheFutonEng/bitcoin-prometheus-exporter/internal/rpc"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

type config struct {
	listenAddr    string
	telemetryPath string

	rpcURL        string
	rpcUser       string
	rpcPassword   string
	rpcCookieFile string
	rpcChain      string
	rpcTimeout    time.Duration
	scrapeTimeout time.Duration

	collectorSet     string
	collectorEnable  string
	collectorDisable string

	peerDetail bool
	blockStats bool
	feeTargets string
	feeMode    string
	wallets    string

	logLevel  string
	logFormat string

	showVersion bool
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "bitcoin-exporter:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := parseFlags()

	if cfg.showVersion {
		fmt.Printf("bitcoin-exporter %s (%s %s/%s)\n", buildVersion(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return nil
	}

	logger, err := newLogger(cfg.logLevel, cfg.logFormat)
	if err != nil {
		return err
	}
	slog.SetDefault(logger)

	// Flags are validated before anything touches the node, so a typo is
	// reported as a typo rather than as a connection problem.
	enabled, err := resolveCollectors(cfg)
	if err != nil {
		return err
	}

	feeTargets, err := parseIntList(cfg.feeTargets)
	if err != nil {
		return fmt.Errorf("-collector.fees.targets: %w", err)
	}
	if cfg.feeMode != "conservative" && cfg.feeMode != "economical" {
		return fmt.Errorf("-collector.fees.mode must be conservative or economical, got %q", cfg.feeMode)
	}

	auth, err := rpc.ResolveAuth(cfg.rpcUser, cfg.rpcPassword, cfg.rpcCookieFile, cfg.rpcChain)
	if err != nil {
		return err
	}

	client, err := rpc.New(rpc.Options{URL: cfg.rpcURL, Auth: auth, Timeout: cfg.rpcTimeout})
	if err != nil {
		return err
	}

	exporter, err := collector.NewExporter(client, enabled, collector.Config{
		FeeTargets: feeTargets,
		FeeMode:    cfg.feeMode,
		PeerDetail: cfg.peerDetail,
		BlockStats: cfg.blockStats,
		Wallets:    splitList(cfg.wallets),
		Logger:     logger,
	}, cfg.scrapeTimeout)
	if err != nil {
		return err
	}
	// The exporter publishes the client's own call latency, so it is wired up
	// as the client's observer once both exist.
	client.SetObserver(exporter)

	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		buildInfoCollector(),
		exporter,
	)

	mux := http.NewServeMux()
	mux.Handle(cfg.telemetryPath, promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		ErrorLog:            slog.NewLogLogger(logger.Handler(), slog.LevelError),
		ErrorHandling:       promhttp.ContinueOnError,
		EnableOpenMetrics:   true,
		MaxRequestsInFlight: 4,
	}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, landingPage, cfg.telemetryPath, buildVersion(), client.Endpoint(), strings.Join(enabled, ", "))
	})

	srv := &http.Server{
		Addr:              cfg.listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	logger.Info("starting bitcoin-exporter",
		"version", buildVersion(),
		"listen", cfg.listenAddr,
		"path", cfg.telemetryPath,
		"rpc", client.Endpoint(),
		"auth", auth.Describe(),
		"collectors", strings.Join(enabled, ","),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func parseFlags() *config {
	cfg := &config{}

	flag.StringVar(&cfg.listenAddr, "web.listen-address", envOr("BITCOIN_EXPORTER_LISTEN", ":9332"),
		"Address to serve metrics on.")
	flag.StringVar(&cfg.telemetryPath, "web.telemetry-path", "/metrics",
		"Path under which to expose metrics.")

	flag.StringVar(&cfg.rpcURL, "rpc.url", envOr("BITCOIN_RPC_URL", "http://127.0.0.1:8332"),
		"Base URL of the node's JSON-RPC endpoint.")
	flag.StringVar(&cfg.rpcUser, "rpc.user", os.Getenv("BITCOIN_RPC_USER"),
		"RPC username. Overrides cookie authentication when set with -rpc.password.")
	flag.StringVar(&cfg.rpcPassword, "rpc.password", os.Getenv("BITCOIN_RPC_PASSWORD"),
		"RPC password. Prefer the BITCOIN_RPC_PASSWORD environment variable.")
	flag.StringVar(&cfg.rpcCookieFile, "rpc.cookie-file", os.Getenv("BITCOIN_RPC_COOKIE_FILE"),
		"Path to the node's .cookie file. Probed under the usual data directories when empty.")
	flag.StringVar(&cfg.rpcChain, "rpc.chain", envOr("BITCOIN_RPC_CHAIN", "main"),
		"Chain the node runs on (main, testnet3, testnet4, signet, regtest). Only used to locate the cookie file.")
	flag.DurationVar(&cfg.rpcTimeout, "rpc.timeout", 5*time.Second,
		"Timeout for a single RPC call.")
	flag.DurationVar(&cfg.scrapeTimeout, "scrape.timeout", 10*time.Second,
		"Overall deadline for one scrape.")

	flag.StringVar(&cfg.collectorSet, "collectors", "",
		"Comma separated collectors to run, replacing the defaults. Use 'all' for every collector, or leave empty for the defaults.")
	flag.StringVar(&cfg.collectorEnable, "collector.enable", "",
		"Comma separated collectors to add to the active set.")
	flag.StringVar(&cfg.collectorDisable, "collector.disable", "",
		"Comma separated collectors to remove from the active set.")

	flag.BoolVar(&cfg.peerDetail, "collector.peers.detail", false,
		"Publish one series per connected peer. Increases cardinality with peer count.")
	flag.BoolVar(&cfg.blockStats, "collector.mining.blockstats", true,
		"Query getblockstats for the chain tip. Disable on pruned nodes or to reduce node load.")
	flag.StringVar(&cfg.feeTargets, "collector.fees.targets", joinInts(collector.DefaultFeeTargets),
		"Comma separated confirmation targets for estimatesmartfee.")
	flag.StringVar(&cfg.feeMode, "collector.fees.mode", "conservative",
		"estimatesmartfee estimate mode: conservative or economical.")
	flag.StringVar(&cfg.wallets, "collector.wallet.names", "",
		"Comma separated wallets to report on. Empty means every loaded wallet.")

	flag.StringVar(&cfg.logLevel, "log.level", "info", "Log level: debug, info, warn or error.")
	flag.StringVar(&cfg.logFormat, "log.format", "logfmt", "Log format: logfmt or json.")
	flag.BoolVar(&cfg.showVersion, "version", false, "Print the version and exit.")

	flag.Usage = usage
	flag.Parse()
	return cfg
}

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintf(out, "bitcoin-exporter %s - Prometheus metrics for a Bitcoin Core node\n\nUsage:\n  bitcoin-exporter [flags]\n\nFlags:\n", buildVersion())
	flag.PrintDefaults()
	fmt.Fprintf(out, "\nCollectors:\n")
	for _, name := range collector.Registered() {
		state := "off by default"
		if collector.DefaultEnabled(name) {
			state = "on by default"
		}
		fmt.Fprintf(out, "  %-10s %s (%s)\n", name, collector.Help(name), state)
	}
}

// resolveCollectors turns the three collector flags into the final active set.
func resolveCollectors(cfg *config) ([]string, error) {
	known := collector.Registered()
	active := map[string]bool{}

	switch base := strings.TrimSpace(cfg.collectorSet); {
	case base == "":
		for _, name := range known {
			active[name] = collector.DefaultEnabled(name)
		}
	case base == "all":
		for _, name := range known {
			active[name] = true
		}
	case base == "none":
		// Start empty and let -collector.enable build the set up.
	default:
		for _, name := range splitList(base) {
			if !slices.Contains(known, name) {
				return nil, fmt.Errorf("-collectors: unknown collector %q (known: %s)", name, strings.Join(known, ", "))
			}
			active[name] = true
		}
	}

	for _, name := range splitList(cfg.collectorEnable) {
		if !slices.Contains(known, name) {
			return nil, fmt.Errorf("-collector.enable: unknown collector %q (known: %s)", name, strings.Join(known, ", "))
		}
		active[name] = true
	}
	for _, name := range splitList(cfg.collectorDisable) {
		if !slices.Contains(known, name) {
			return nil, fmt.Errorf("-collector.disable: unknown collector %q (known: %s)", name, strings.Join(known, ", "))
		}
		active[name] = false
	}

	var enabled []string
	for name, on := range active {
		if on {
			enabled = append(enabled, name)
		}
	}
	if len(enabled) == 0 {
		return nil, errors.New("no collectors enabled")
	}
	sort.Strings(enabled)
	return enabled, nil
}

func buildInfoCollector() prometheus.Collector {
	revision, buildDate := "unknown", "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.time":
				buildDate = setting.Value
			}
		}
	}
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: collector.Namespace,
		Subsystem: "exporter",
		Name:      "build_info",
		Help:      "Always 1, labelled with the build identity of the running exporter.",
	}, []string{"version", "revision", "build_date", "goversion"})
	g.WithLabelValues(buildVersion(), revision, buildDate, runtime.Version()).Set(1)
	return g
}

func buildVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

func newLogger(level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("-log.level: %w", err)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	switch format {
	case "json":
		return slog.New(slog.NewJSONHandler(os.Stderr, opts)), nil
	case "logfmt", "text":
		return slog.New(slog.NewTextHandler(os.Stderr, opts)), nil
	default:
		return nil, fmt.Errorf("-log.format: want logfmt or json, got %q", format)
	}
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseIntList(s string) ([]int, error) {
	var out []int
	for _, part := range splitList(s) {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", part)
		}
		out = append(out, n)
	}
	return out, nil
}

func joinInts(vals []int) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ",")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

const landingPage = `<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>Bitcoin Exporter</title>
<style>
 body{font-family:system-ui,sans-serif;margin:3rem auto;max-width:42rem;line-height:1.5;color:#1c1c1c}
 code{background:#f2f2f2;padding:.1rem .3rem;border-radius:3px}
 dt{font-weight:600;margin-top:.6rem}
 @media (prefers-color-scheme:dark){body{background:#151515;color:#e8e8e8}code{background:#2a2a2a}a{color:#7cc3ff}}
</style></head>
<body>
<h1>Bitcoin Exporter</h1>
<p><a href="%s">Metrics</a> &middot; <a href="/healthz">Health</a></p>
<dl>
 <dt>Version</dt><dd><code>%s</code></dd>
 <dt>Node endpoint</dt><dd><code>%s</code></dd>
 <dt>Collectors</dt><dd><code>%s</code></dd>
</dl>
</body></html>
`
