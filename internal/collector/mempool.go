package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TheFutonEng/bitcoin-prometheus-exporter/internal/rpc"
)

func init() {
	register("mempool", "Mempool occupancy, memory usage and minimum fee (getmempoolinfo).", true,
		func(c *rpc.Client, cfg Config) (Collector, error) { return &mempoolCollector{client: c}, nil })
}

type mempoolInfo struct {
	Loaded           *bool   `json:"loaded"`
	Size             float64 `json:"size"`
	Bytes            float64 `json:"bytes"`
	Usage            float64 `json:"usage"`
	TotalFee         float64 `json:"total_fee"`
	MaxMempool       float64 `json:"maxmempool"`
	MempoolMinFee    float64 `json:"mempoolminfee"`
	MinRelayTxFee    float64 `json:"minrelaytxfee"`
	UnbroadcastCount float64 `json:"unbroadcastcount"`
	FullRBF          *bool   `json:"fullrbf"`
}

type mempoolCollector struct{ client *rpc.Client }

var (
	descMempoolLoaded      = desc("mempool_loaded", "1 once the mempool has finished loading from disk.")
	descMempoolTxs         = desc("mempool_txs", "Number of transactions currently in the mempool.")
	descMempoolBytes       = desc("mempool_bytes", "Sum of the virtual sizes of all mempool transactions.")
	descMempoolUsage       = desc("mempool_usage_bytes", "Memory the mempool occupies in the node process.")
	descMempoolMax         = desc("mempool_max_bytes", "Configured -maxmempool memory ceiling.")
	descMempoolFee         = desc("mempool_total_fee_btc", "Sum of the fees of all mempool transactions, in BTC.")
	descMempoolMinFee      = desc("mempool_min_fee_btc_per_kvb", "Lowest fee rate the mempool currently accepts, in BTC/kvB.")
	descMempoolMinFeeSat   = desc("mempool_min_fee_sat_per_vbyte", "Lowest fee rate the mempool currently accepts, in sat/vB.")
	descMempoolRelayFee    = desc("mempool_min_relay_fee_btc_per_kvb", "Configured -minrelaytxfee floor, in BTC/kvB.")
	descMempoolRelayFeeSat = desc("mempool_min_relay_fee_sat_per_vbyte", "Configured -minrelaytxfee floor, in sat/vB.")
	descMempoolUnbroadcast = desc("mempool_unbroadcast_txs", "Transactions the node has not yet seen acknowledged by a peer.")
	descMempoolFullRBF     = desc("mempool_full_rbf", "1 if the node accepts replacement of any transaction regardless of opt-in.")
	descMempoolFillRatio   = desc("mempool_fill_ratio", "Mempool memory usage divided by -maxmempool, 0 to 1.")
)

func (c *mempoolCollector) Update(ctx context.Context, ch chan<- prometheus.Metric) error {
	var info mempoolInfo
	if err := c.client.Call(ctx, "getmempoolinfo", nil, &info); err != nil {
		return err
	}

	if v, ok := boolPtrToFloat(info.Loaded); ok {
		gauge(ch, descMempoolLoaded, v)
	}
	gauge(ch, descMempoolTxs, info.Size)
	gauge(ch, descMempoolBytes, info.Bytes)
	gauge(ch, descMempoolUsage, info.Usage)
	gauge(ch, descMempoolMax, info.MaxMempool)
	gauge(ch, descMempoolFee, info.TotalFee)
	gauge(ch, descMempoolMinFee, info.MempoolMinFee)
	gauge(ch, descMempoolMinFeeSat, satPerVByte(info.MempoolMinFee))
	gauge(ch, descMempoolRelayFee, info.MinRelayTxFee)
	gauge(ch, descMempoolRelayFeeSat, satPerVByte(info.MinRelayTxFee))
	gauge(ch, descMempoolUnbroadcast, info.UnbroadcastCount)
	if v, ok := boolPtrToFloat(info.FullRBF); ok {
		gauge(ch, descMempoolFullRBF, v)
	}
	if info.MaxMempool > 0 {
		gauge(ch, descMempoolFillRatio, info.Usage/info.MaxMempool)
	}
	return nil
}
