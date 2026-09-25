package collector

import (
	"context"
	"encoding/json"
	"math/big"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TheFutonEng/bitcoin-prometheus-exporter/internal/rpc"
)

func init() {
	register("chain", "Chain tip, sync progress and on-disk size (getblockchaininfo).", true,
		func(c *rpc.Client, cfg Config) (Collector, error) { return &chainCollector{client: c}, nil })
}

// warnings decodes the node's warning field, which is a bare string on older
// releases and an array of strings from Bitcoin Core 25 onwards.
type warnings []string

func (w *warnings) UnmarshalJSON(b []byte) error {
	var list []string
	if err := json.Unmarshal(b, &list); err == nil {
		*w = list
		return nil
	}
	var single string
	if err := json.Unmarshal(b, &single); err != nil {
		return err
	}
	if single == "" {
		*w = nil
		return nil
	}
	*w = []string{single}
	return nil
}

type blockchainInfo struct {
	Chain                string   `json:"chain"`
	Blocks               int64    `json:"blocks"`
	Headers              int64    `json:"headers"`
	BestBlockHash        string   `json:"bestblockhash"`
	Difficulty           float64  `json:"difficulty"`
	Time                 *int64   `json:"time"`
	MedianTime           *int64   `json:"mediantime"`
	VerificationProgress float64  `json:"verificationprogress"`
	InitialBlockDownload bool     `json:"initialblockdownload"`
	ChainWork            string   `json:"chainwork"`
	SizeOnDisk           float64  `json:"size_on_disk"`
	Pruned               bool     `json:"pruned"`
	PruneHeight          *int64   `json:"pruneheight"`
	AutomaticPruning     *bool    `json:"automatic_pruning"`
	PruneTargetSize      *int64   `json:"prune_target_size"`
	Warnings             warnings `json:"warnings"`
}

type chainCollector struct{ client *rpc.Client }

var (
	descChainInfo  = desc("chain_info", "Always 1, labelled with the chain the node is running on.", "chain")
	descBlocks     = desc("blocks", "Height of the most-work fully validated chain.")
	descHeaders    = desc("headers", "Height of the best known block header.")
	descBlocksLeft = desc("blocks_behind", "Known headers minus validated blocks; 0 when the node is at the tip.")
	descDifficulty = desc("difficulty", "Proof-of-work difficulty of the current best block.")
	descVerifyProg = desc("verification_progress", "Estimated share of chain work verified so far, 0 to 1.")
	descIBD        = desc("initial_block_download", "1 while the node considers itself in initial block download.")
	descChainWork  = desc("chainwork", "Total cumulative proof of work on the best chain, as a float approximation.")
	descTipTime    = desc("best_block_time_seconds", "Unix timestamp of the current best block.")
	descMedianTime = desc("median_time_seconds", "Median unix timestamp of the last 11 blocks.")
	descSizeOnDisk = desc("size_on_disk_bytes", "Estimated size of the block and undo files on disk.")
	descPruned     = desc("pruned", "1 if the node is running with block pruning enabled.")
	descPruneAuto  = desc("prune_automatic", "1 if pruning is managed automatically from a target size.")
	descPruneTgt   = desc("prune_target_size_bytes", "Target size the node prunes the block store down to.")
	descPruneH     = desc("prune_height", "Lowest block height for which full block data is still stored.")
	descWarnings   = desc("warnings", "Number of warnings the node is currently reporting.")
	descWarnInfo   = desc("warning_info", "Always 1, labelled with a warning the node is currently reporting.", "warning")
)

func (c *chainCollector) Update(ctx context.Context, ch chan<- prometheus.Metric) error {
	var info blockchainInfo
	if err := c.client.Call(ctx, "getblockchaininfo", nil, &info); err != nil {
		return err
	}

	gauge(ch, descChainInfo, 1, info.Chain)
	gauge(ch, descBlocks, float64(info.Blocks))
	gauge(ch, descHeaders, float64(info.Headers))
	if behind := info.Headers - info.Blocks; behind >= 0 {
		gauge(ch, descBlocksLeft, float64(behind))
	}
	gauge(ch, descDifficulty, info.Difficulty)
	gauge(ch, descVerifyProg, info.VerificationProgress)
	gauge(ch, descIBD, boolToFloat(info.InitialBlockDownload))
	if work, ok := parseChainWork(info.ChainWork); ok {
		gauge(ch, descChainWork, work)
	}
	gaugePtr(ch, descTipTime, info.Time)
	gaugePtr(ch, descMedianTime, info.MedianTime)
	gauge(ch, descSizeOnDisk, info.SizeOnDisk)
	gauge(ch, descPruned, boolToFloat(info.Pruned))
	if v, ok := boolPtrToFloat(info.AutomaticPruning); ok {
		gauge(ch, descPruneAuto, v)
	}
	gaugePtr(ch, descPruneTgt, info.PruneTargetSize)
	gaugePtr(ch, descPruneH, info.PruneHeight)

	gauge(ch, descWarnings, float64(len(info.Warnings)))
	for _, w := range info.Warnings {
		gauge(ch, descWarnInfo, 1, w)
	}
	return nil
}

// parseChainWork converts the 256-bit hex chainwork value into a float64. The
// magnitude is what matters on a graph, so the lost precision is acceptable.
func parseChainWork(hex string) (float64, bool) {
	if hex == "" {
		return 0, false
	}
	n, ok := new(big.Int).SetString(hex, 16)
	if !ok {
		return 0, false
	}
	f, _ := new(big.Float).SetInt(n).Float64()
	return f, true
}
