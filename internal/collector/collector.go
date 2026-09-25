// Package collector turns Bitcoin Core RPC responses into Prometheus metrics.
package collector

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TheFutonEng/bitcoin-prometheus-exporter/internal/rpc"
)

// Namespace prefixes every metric this exporter publishes.
const Namespace = "bitcoin"

// Collector gathers one family of node metrics. Update is called once per
// scrape and must be safe for concurrent use.
type Collector interface {
	// Update writes the collector's metrics to ch.
	Update(ctx context.Context, ch chan<- prometheus.Metric) error
}

// Config carries the settings individual collectors read at construction.
type Config struct {
	// FeeTargets are the confirmation targets passed to estimatesmartfee.
	FeeTargets []int
	// FeeMode is the estimatesmartfee estimate_mode ("conservative" or "economical").
	FeeMode string
	// PeerDetail publishes one metric series per connected peer.
	PeerDetail bool
	// BlockStats queries getblockstats for the chain tip.
	BlockStats bool
	// Wallets limits the wallet collector to these names; empty means every
	// loaded wallet.
	Wallets []string
	// Logger receives non-fatal collector diagnostics.
	Logger *slog.Logger
}

// Factory builds a Collector against a node client.
type Factory func(client *rpc.Client, cfg Config) (Collector, error)

type registration struct {
	name           string
	help           string
	enabledDefault bool
	factory        Factory
}

var registry = map[string]registration{}

func register(name, help string, enabledDefault bool, factory Factory) {
	registry[name] = registration{name: name, help: help, enabledDefault: enabledDefault, factory: factory}
}

// Registered returns every known collector name in stable order.
func Registered() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DefaultEnabled reports whether a collector runs unless explicitly disabled.
func DefaultEnabled(name string) bool { return registry[name].enabledDefault }

// Help returns a collector's one-line description.
func Help(name string) string { return registry[name].help }

// Exporter is the prometheus.Collector that drives every enabled sub-collector.
type Exporter struct {
	client     *rpc.Client
	collectors map[string]Collector
	timeout    time.Duration
	logger     *slog.Logger

	up              *prometheus.Desc
	scrapeDuration  *prometheus.Desc
	scrapeSuccess   *prometheus.Desc
	rpcDuration     *prometheus.HistogramVec
	rpcErrorsTotal  *prometheus.CounterVec
	rpcRequestTotal *prometheus.CounterVec
}

// NewExporter builds an Exporter running the named collectors.
func NewExporter(client *rpc.Client, enabled []string, cfg Config, timeout time.Duration) (*Exporter, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	collectors := make(map[string]Collector, len(enabled))
	for _, name := range enabled {
		reg, ok := registry[name]
		if !ok {
			return nil, fmt.Errorf("unknown collector %q (known: %v)", name, Registered())
		}
		c, err := reg.factory(client, cfg)
		if err != nil {
			return nil, fmt.Errorf("build collector %q: %w", name, err)
		}
		collectors[name] = c
	}

	return &Exporter{
		client:     client,
		collectors: collectors,
		timeout:    timeout,
		logger:     cfg.Logger,
		up: prometheus.NewDesc(
			prometheus.BuildFQName(Namespace, "", "up"),
			"1 if the node answered RPC during this scrape, 0 otherwise.",
			nil, nil,
		),
		scrapeDuration: prometheus.NewDesc(
			prometheus.BuildFQName(Namespace, "exporter", "collector_duration_seconds"),
			"Time each collector spent gathering metrics during the last scrape.",
			[]string{"collector"}, nil,
		),
		scrapeSuccess: prometheus.NewDesc(
			prometheus.BuildFQName(Namespace, "exporter", "collector_success"),
			"1 if the collector completed without error during the last scrape.",
			[]string{"collector"}, nil,
		),
		rpcDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace,
			Subsystem: "exporter",
			Name:      "rpc_duration_seconds",
			Help:      "Latency of JSON-RPC calls to the node.",
			Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"method"}),
		rpcRequestTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Subsystem: "exporter",
			Name:      "rpc_requests_total",
			Help:      "Total JSON-RPC calls made to the node.",
		}, []string{"method"}),
		rpcErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Subsystem: "exporter",
			Name:      "rpc_errors_total",
			Help:      "Total JSON-RPC calls that returned an error.",
		}, []string{"method"}),
	}, nil
}

// ObserveRPC implements rpc.Observer.
func (e *Exporter) ObserveRPC(method string, dur time.Duration, err error) {
	e.rpcDuration.WithLabelValues(method).Observe(dur.Seconds())
	e.rpcRequestTotal.WithLabelValues(method).Inc()
	if err != nil {
		e.rpcErrorsTotal.WithLabelValues(method).Inc()
	}
}

// Describe implements prometheus.Collector.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- e.up
	ch <- e.scrapeDuration
	ch <- e.scrapeSuccess
	for _, d := range allDescs {
		ch <- d
	}
	e.rpcDuration.Describe(ch)
	e.rpcRequestTotal.Describe(ch)
	e.rpcErrorsTotal.Describe(ch)
}

// Collect implements prometheus.Collector. Sub-collectors run concurrently;
// one failing collector does not suppress the others.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()

	// A cheap liveness probe decides bitcoin_up independently of whether an
	// individual collector happens to be failing.
	upErr := e.client.Call(ctx, "uptime", nil, nil)
	if upErr != nil && rpc.Code(upErr) != 0 {
		// The node answered, it just disliked the call: still up.
		upErr = nil
	}
	ch <- prometheus.MustNewConstMetric(e.up, prometheus.GaugeValue, boolToFloat(upErr == nil))
	if upErr != nil {
		e.logger.Warn("node unreachable", "endpoint", e.client.Endpoint(), "err", upErr)
	}

	var wg sync.WaitGroup
	for name, c := range e.collectors {
		wg.Add(1)
		go func(name string, c Collector) {
			defer wg.Done()
			start := time.Now()
			err := c.Update(ctx, ch)
			dur := time.Since(start)
			if err != nil {
				e.logger.Warn("collector failed", "collector", name, "err", err)
			}
			ch <- prometheus.MustNewConstMetric(e.scrapeDuration, prometheus.GaugeValue, dur.Seconds(), name)
			ch <- prometheus.MustNewConstMetric(e.scrapeSuccess, prometheus.GaugeValue, boolToFloat(err == nil), name)
		}(name, c)
	}
	wg.Wait()

	e.rpcDuration.Collect(ch)
	e.rpcRequestTotal.Collect(ch)
	e.rpcErrorsTotal.Collect(ch)
}
