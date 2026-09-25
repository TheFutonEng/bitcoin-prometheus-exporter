package collector

import (
	"context"
	"errors"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TheFutonEng/bitcoin-prometheus-exporter/internal/rpc"
)

func init() {
	register("mining", "Network hash rate plus statistics for the block at the tip (getmininginfo, getblockstats).", true,
		func(c *rpc.Client, cfg Config) (Collector, error) {
			return &miningCollector{client: c, blockStats: cfg.BlockStats}, nil
		})
}

type miningInfo struct {
	Blocks             int64    `json:"blocks"`
	CurrentBlockWeight *float64 `json:"currentblockweight"`
	CurrentBlockTx     *float64 `json:"currentblocktx"`
	Difficulty         float64  `json:"difficulty"`
	NetworkHashPS      float64  `json:"networkhashps"`
	PooledTx           float64  `json:"pooledtx"`
}

// blockStats mirrors the subset of getblockstats worth graphing. Fee fields
// are satoshis and fee rates are sat/vB, per bitcoind's own units.
type blockStats struct {
	Height            float64   `json:"height"`
	Time              float64   `json:"time"`
	TotalSize         float64   `json:"total_size"`
	TotalWeight       float64   `json:"total_weight"`
	Txs               float64   `json:"txs"`
	Ins               float64   `json:"ins"`
	Outs              float64   `json:"outs"`
	TotalOut          float64   `json:"total_out"`
	TotalFee          float64   `json:"totalfee"`
	Subsidy           float64   `json:"subsidy"`
	AvgFeeRate        float64   `json:"avgfeerate"`
	MinFeeRate        float64   `json:"minfeerate"`
	MaxFeeRate        float64   `json:"maxfeerate"`
	MedianTxSize      float64   `json:"mediantxsize"`
	AvgTxSize         float64   `json:"avgtxsize"`
	SegwitTxs         float64   `json:"swtxs"`
	SegwitTotalWeight float64   `json:"swtotal_weight"`
	UTXOIncrease      float64   `json:"utxo_increase"`
	FeeratePercentile []float64 `json:"feerate_percentiles"`
}

type miningCollector struct {
	client     *rpc.Client
	blockStats bool
}

var (
	descHashPS       = desc("network_hashps", "Estimated network hash rate in hashes per second.")
	descPooledTx     = desc("mining_pooled_txs", "Transactions the node would consider for the next block template.")
	descCurBlockWt   = desc("mining_current_block_weight", "Weight of the last block template the node assembled.")
	descCurBlockTx   = desc("mining_current_block_txs", "Transaction count of the last block template the node assembled.")
	descBlockStatsOK = desc("latest_block_stats_available", "1 if getblockstats succeeded for the chain tip during this scrape.")
	descBlkHeight    = desc("latest_block_height", "Height of the block the tip statistics describe.")
	descBlkTime      = desc("latest_block_time_seconds", "Unix timestamp of the block at the tip.")
	descBlkSize      = desc("latest_block_size_bytes", "Serialized size of the block at the tip.")
	descBlkWeight    = desc("latest_block_weight", "Weight of the block at the tip.")
	descBlkTxs       = desc("latest_block_txs", "Transaction count of the block at the tip.")
	descBlkIns       = desc("latest_block_inputs", "Number of inputs spent in the block at the tip.")
	descBlkOuts      = desc("latest_block_outputs", "Number of outputs created in the block at the tip.")
	descBlkValue     = desc("latest_block_value_sat", "Total value of the outputs in the block at the tip, in satoshis.")
	descBlkFees      = desc("latest_block_fees_sat", "Total fees paid in the block at the tip, in satoshis.")
	descBlkSubsidy   = desc("latest_block_subsidy_sat", "Block subsidy of the block at the tip, in satoshis.")
	descBlkFeeRate   = desc("latest_block_feerate_sat_per_vbyte", "Fee rate distribution within the block at the tip, in sat/vB.", "stat")
	descBlkTxSize    = desc("latest_block_tx_size_bytes", "Transaction size distribution within the block at the tip.", "stat")
	descBlkSegwit    = desc("latest_block_segwit_txs", "SegWit transactions in the block at the tip.")
	descBlkSegwitWt  = desc("latest_block_segwit_weight", "Weight contributed by SegWit transactions in the block at the tip.")
	descBlkUTXODelta = desc("latest_block_utxo_increase", "Net change in the UTXO set caused by the block at the tip.")
)

// percentileLabels name the feerate_percentiles entries getblockstats returns.
var percentileLabels = []string{"p10", "p25", "p50", "p75", "p90"}

func (c *miningCollector) Update(ctx context.Context, ch chan<- prometheus.Metric) error {
	var info miningInfo
	if err := c.client.Call(ctx, "getmininginfo", nil, &info); err != nil {
		return err
	}
	gauge(ch, descHashPS, info.NetworkHashPS)
	gauge(ch, descPooledTx, info.PooledTx)
	gaugePtr(ch, descCurBlockWt, info.CurrentBlockWeight)
	gaugePtr(ch, descCurBlockTx, info.CurrentBlockTx)

	if !c.blockStats {
		return nil
	}
	if info.Blocks <= 0 {
		// Genesis carries no spendable inputs, so getblockstats has nothing
		// meaningful to report on a freshly initialised regtest chain.
		gauge(ch, descBlockStatsOK, 0)
		return nil
	}

	var stats blockStats
	if err := c.client.Call(ctx, "getblockstats", []any{info.Blocks}, &stats); err != nil {
		gauge(ch, descBlockStatsOK, 0)
		// Pruned nodes and nodes without undo data legitimately refuse this
		// call; only surface transport failures as collector errors.
		if rpc.Code(err) != 0 {
			return nil
		}
		return errors.Join(err)
	}

	gauge(ch, descBlockStatsOK, 1)
	gauge(ch, descBlkHeight, stats.Height)
	gauge(ch, descBlkTime, stats.Time)
	gauge(ch, descBlkSize, stats.TotalSize)
	gauge(ch, descBlkWeight, stats.TotalWeight)
	gauge(ch, descBlkTxs, stats.Txs)
	gauge(ch, descBlkIns, stats.Ins)
	gauge(ch, descBlkOuts, stats.Outs)
	gauge(ch, descBlkValue, stats.TotalOut)
	gauge(ch, descBlkFees, stats.TotalFee)
	gauge(ch, descBlkSubsidy, stats.Subsidy)
	gauge(ch, descBlkSegwit, stats.SegwitTxs)
	gauge(ch, descBlkSegwitWt, stats.SegwitTotalWeight)
	gauge(ch, descBlkUTXODelta, stats.UTXOIncrease)

	gauge(ch, descBlkFeeRate, stats.MinFeeRate, "min")
	gauge(ch, descBlkFeeRate, stats.AvgFeeRate, "avg")
	gauge(ch, descBlkFeeRate, stats.MaxFeeRate, "max")
	for i, v := range stats.FeeratePercentile {
		if i < len(percentileLabels) {
			gauge(ch, descBlkFeeRate, v, percentileLabels[i])
		}
	}
	gauge(ch, descBlkTxSize, stats.AvgTxSize, "avg")
	gauge(ch, descBlkTxSize, stats.MedianTxSize, "median")
	return nil
}
