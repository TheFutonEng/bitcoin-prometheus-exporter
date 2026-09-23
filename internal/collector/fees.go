package collector

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TheFutonEng/bitcoin-prometheus-exporter/internal/rpc"
)

func init() {
	register("fees", "Fee rate estimates for several confirmation targets (estimatesmartfee).", true,
		func(c *rpc.Client, cfg Config) (Collector, error) {
			targets := cfg.FeeTargets
			if len(targets) == 0 {
				targets = DefaultFeeTargets
			}
			mode := cfg.FeeMode
			if mode == "" {
				mode = "conservative"
			}
			for _, t := range targets {
				if t < 1 || t > 1008 {
					return nil, fmt.Errorf("fee target %d out of range (1-1008)", t)
				}
			}
			return &feeCollector{client: c, targets: targets, mode: mode}, nil
		})
}

// DefaultFeeTargets are the confirmation targets queried unless overridden:
// next block, half hour, hour, quarter day, day and week.
var DefaultFeeTargets = []int{1, 2, 3, 6, 36, 144, 1008}

type feeEstimate struct {
	FeeRate *float64 `json:"feerate"`
	Errors  []string `json:"errors"`
	Blocks  int      `json:"blocks"`
}

type feeCollector struct {
	client  *rpc.Client
	targets []int
	mode    string
}

var (
	descFeeEstimate    = desc("fee_estimate_btc_per_kvb", "Estimated fee rate to confirm within N blocks, in BTC/kvB.", "blocks", "mode")
	descFeeEstimateSat = desc("fee_estimate_sat_per_vbyte", "Estimated fee rate to confirm within N blocks, in sat/vB.", "blocks", "mode")
	descFeeAvailable   = desc("fee_estimate_available", "1 if the node had enough data to answer estimatesmartfee for this target.", "blocks", "mode")
	descFeeAnswered    = desc("fee_estimate_answered_blocks", "Confirmation target the estimate actually corresponds to, which may exceed the requested one.", "blocks", "mode")
)

func (c *feeCollector) Update(ctx context.Context, ch chan<- prometheus.Metric) error {
	var errs []error
	for _, target := range c.targets {
		label := strconv.Itoa(target)

		var est feeEstimate
		if err := c.client.Call(ctx, "estimatesmartfee", []any{target, c.mode}, &est); err != nil {
			// A node built without fee estimation, or one that has never seen
			// a block, is not an exporter failure worth flagging every scrape.
			if rpc.Code(err) != 0 {
				gauge(ch, descFeeAvailable, 0, label, c.mode)
				continue
			}
			errs = append(errs, err)
			continue
		}

		// bitcoind reports "insufficient data" in-band rather than as an RPC
		// error, which is the normal state on regtest and on a fresh node.
		if est.FeeRate == nil {
			gauge(ch, descFeeAvailable, 0, label, c.mode)
			continue
		}
		gauge(ch, descFeeAvailable, 1, label, c.mode)
		gauge(ch, descFeeEstimate, *est.FeeRate, label, c.mode)
		gauge(ch, descFeeEstimateSat, satPerVByte(*est.FeeRate), label, c.mode)
		if est.Blocks > 0 {
			gauge(ch, descFeeAnswered, float64(est.Blocks), label, c.mode)
		}
	}
	return errors.Join(errs...)
}
