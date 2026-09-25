package collector

import (
	"context"
	"errors"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TheFutonEng/bitcoin-prometheus-exporter/internal/rpc"
)

func init() {
	register("network", "Connection counts, traffic totals and node version (getnetworkinfo, getnettotals, uptime).", true,
		func(c *rpc.Client, cfg Config) (Collector, error) { return &networkCollector{client: c}, nil })
}

type networkInfo struct {
	Version         int64    `json:"version"`
	Subversion      string   `json:"subversion"`
	ProtocolVersion int64    `json:"protocolversion"`
	LocalRelay      bool     `json:"localrelay"`
	TimeOffset      float64  `json:"timeoffset"`
	Connections     float64  `json:"connections"`
	ConnectionsIn   *float64 `json:"connections_in"`
	ConnectionsOut  *float64 `json:"connections_out"`
	NetworkActive   bool     `json:"networkactive"`
	RelayFee        float64  `json:"relayfee"`
	IncrementalFee  float64  `json:"incrementalfee"`
	Networks        []struct {
		Name      string `json:"name"`
		Limited   bool   `json:"limited"`
		Reachable bool   `json:"reachable"`
		Proxy     string `json:"proxy"`
	} `json:"networks"`
}

type netTotals struct {
	TotalBytesRecv float64 `json:"totalbytesrecv"`
	TotalBytesSent float64 `json:"totalbytessent"`
	UploadTarget   struct {
		Timeframe            float64 `json:"timeframe"`
		Target               float64 `json:"target"`
		TargetReached        bool    `json:"target_reached"`
		ServeHistoricalBlock bool    `json:"serve_historical_blocks"`
		BytesLeftInCycle     float64 `json:"bytes_left_in_cycle"`
		TimeLeftInCycle      float64 `json:"time_left_in_cycle"`
	} `json:"uploadtarget"`
}

type networkCollector struct{ client *rpc.Client }

var (
	descVersionInfo    = desc("version_info", "Always 1, labelled with the node's version strings.", "version", "subversion", "protocol_version")
	descConnections    = desc("connections", "Total peer connections the node currently holds.")
	descPeersDirection = desc("peers", "Peer connections by direction.", "direction")
	descNetworkActive  = desc("network_active", "1 if p2p networking is enabled.")
	descLocalRelay     = desc("local_relay", "1 if the node relays transactions from its own mempool.")
	descTimeOffset     = desc("time_offset_seconds", "Node clock offset from the median of its peers.")
	descRelayFee       = desc("relay_fee_btc_per_kvb", "Minimum fee rate for a transaction to be relayed, in BTC/kvB.")
	descRelayFeeSat    = desc("relay_fee_sat_per_vbyte", "Minimum fee rate for a transaction to be relayed, in sat/vB.")
	descIncFee         = desc("incremental_fee_btc_per_kvb", "Minimum fee rate increment for replacement and mempool limiting, in BTC/kvB.")
	descIncFeeSat      = desc("incremental_fee_sat_per_vbyte", "Minimum fee rate increment for replacement and mempool limiting, in sat/vB.")
	descNetReachable   = desc("network_reachable", "1 if the node considers this network reachable.", "network")
	descNetLimited     = desc("network_limited", "1 if the node will not make outbound connections on this network.", "network")
	descNetProxy       = desc("network_proxy_info", "Always 1, labelled with the proxy configured for a network.", "network", "proxy")
	descNetBytes       = desc("net_bytes_total", "Cumulative p2p traffic since the node started.", "direction")
	descUploadTarget   = desc("upload_target_bytes", "Configured -maxuploadtarget for one cycle; 0 when unset.")
	descUploadReached  = desc("upload_target_reached", "1 if the upload target for the current cycle has been hit.")
	descUploadLeft     = desc("upload_target_bytes_left", "Bytes left in the current upload target cycle.")
	descUploadTimeLeft = desc("upload_target_seconds_left", "Seconds left in the current upload target cycle.")
	descUploadHist     = desc("upload_target_serve_historical_blocks", "1 if the node still serves historical blocks under the upload target.")
	descUptime         = desc("uptime_seconds", "Seconds the node process has been running.")
)

func (c *networkCollector) Update(ctx context.Context, ch chan<- prometheus.Metric) error {
	var errs []error

	var info networkInfo
	if err := c.client.Call(ctx, "getnetworkinfo", nil, &info); err != nil {
		errs = append(errs, err)
	} else {
		gauge(ch, descVersionInfo, 1,
			strconv.FormatInt(info.Version, 10), info.Subversion, strconv.FormatInt(info.ProtocolVersion, 10))
		gauge(ch, descConnections, info.Connections)
		gaugePtr(ch, descPeersDirection, info.ConnectionsIn, "inbound")
		gaugePtr(ch, descPeersDirection, info.ConnectionsOut, "outbound")
		gauge(ch, descNetworkActive, boolToFloat(info.NetworkActive))
		gauge(ch, descLocalRelay, boolToFloat(info.LocalRelay))
		gauge(ch, descTimeOffset, info.TimeOffset)
		gauge(ch, descRelayFee, info.RelayFee)
		gauge(ch, descRelayFeeSat, satPerVByte(info.RelayFee))
		gauge(ch, descIncFee, info.IncrementalFee)
		gauge(ch, descIncFeeSat, satPerVByte(info.IncrementalFee))
		for _, n := range info.Networks {
			gauge(ch, descNetReachable, boolToFloat(n.Reachable), n.Name)
			gauge(ch, descNetLimited, boolToFloat(n.Limited), n.Name)
			if n.Proxy != "" {
				gauge(ch, descNetProxy, 1, n.Name, n.Proxy)
			}
		}
	}

	var totals netTotals
	if err := c.client.Call(ctx, "getnettotals", nil, &totals); err != nil {
		errs = append(errs, err)
	} else {
		counter(ch, descNetBytes, totals.TotalBytesSent, "sent")
		counter(ch, descNetBytes, totals.TotalBytesRecv, "received")
		t := totals.UploadTarget
		gauge(ch, descUploadTarget, t.Target)
		gauge(ch, descUploadReached, boolToFloat(t.TargetReached))
		gauge(ch, descUploadLeft, t.BytesLeftInCycle)
		gauge(ch, descUploadTimeLeft, t.TimeLeftInCycle)
		gauge(ch, descUploadHist, boolToFloat(t.ServeHistoricalBlock))
	}

	var uptime float64
	if err := c.client.Call(ctx, "uptime", nil, &uptime); err != nil {
		errs = append(errs, err)
	} else {
		gauge(ch, descUptime, uptime)
	}

	return errors.Join(errs...)
}
