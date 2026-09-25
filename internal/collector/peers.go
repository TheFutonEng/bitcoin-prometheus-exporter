package collector

import (
	"context"
	"errors"
	"math"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TheFutonEng/bitcoin-prometheus-exporter/internal/rpc"
)

func init() {
	register("peers", "Peer breakdown by network, version and connection type (getpeerinfo, listbanned).", true,
		func(c *rpc.Client, cfg Config) (Collector, error) {
			return &peerCollector{client: c, detail: cfg.PeerDetail}, nil
		})
}

type peerInfo struct {
	ID                int64    `json:"id"`
	Addr              string   `json:"addr"`
	Network           string   `json:"network"`
	Version           int64    `json:"version"`
	SubVer            string   `json:"subver"`
	Inbound           bool     `json:"inbound"`
	ConnectionType    string   `json:"connection_type"`
	TransportProtocol string   `json:"transport_protocol_type"`
	PingTime          *float64 `json:"pingtime"`
	MinPing           *float64 `json:"minping"`
	PingWait          *float64 `json:"pingwait"`
	BytesSent         float64  `json:"bytessent"`
	BytesRecv         float64  `json:"bytesrecv"`
	ConnTime          float64  `json:"conntime"`
	LastSend          float64  `json:"lastsend"`
	LastRecv          float64  `json:"lastrecv"`
	LastTransaction   *float64 `json:"last_transaction"`
	LastBlock         *float64 `json:"last_block"`
	StartingHeight    *float64 `json:"startingheight"`
	SyncedHeaders     *float64 `json:"synced_headers"`
	SyncedBlocks      *float64 `json:"synced_blocks"`
	MinFeeFilter      *float64 `json:"minfeefilter"`
	TimeOffset        *float64 `json:"timeoffset"`
	RelayTxes         *bool    `json:"relaytxes"`
	AddrProcessed     *float64 `json:"addr_processed"`
	AddrRateLimited   *float64 `json:"addr_rate_limited"`
	HighBandwidthTo   *bool    `json:"bip152_hb_to"`
	HighBandwidthFrom *bool    `json:"bip152_hb_from"`
}

type bannedEntry struct {
	Address     string  `json:"address"`
	BannedUntil float64 `json:"banned_until"`
}

type peerCollector struct {
	client *rpc.Client
	detail bool
}

var (
	descPeersByNetwork   = desc("peers_by_network", "Connected peers grouped by the network they are reachable on.", "network")
	descPeersBySubver    = desc("peers_by_subversion", "Connected peers grouped by their advertised user agent.", "subversion")
	descPeersByConnType  = desc("peers_by_connection_type", "Connected peers grouped by how the connection was established.", "connection_type")
	descPeersByTransport = desc("peers_by_transport", "Connected peers grouped by p2p transport protocol.", "transport")
	descPeersPing        = desc("peers_ping_seconds", "Aggregate round-trip time across connected peers.", "stat")
	descPeersBanned      = desc("peers_banned", "Number of entries in the node's ban list.")
	descPeersRelaying    = desc("peers_relaying_txs", "Connected peers the node relays transactions to.")
	descPeersHighBW      = desc("peers_high_bandwidth", "Peers in a BIP152 high-bandwidth compact block relationship, by direction.", "direction")

	descPeerInfo      = desc("peer_info", "Always 1, labelled with the identifying attributes of one peer.", "id", "addr", "network", "direction", "connection_type", "subversion", "version")
	descPeerPing      = desc("peer_ping_seconds", "Last measured round-trip time to this peer.", "id", "addr")
	descPeerMinPing   = desc("peer_min_ping_seconds", "Best round-trip time seen for this peer.", "id", "addr")
	descPeerPingWait  = desc("peer_ping_wait_seconds", "Time spent waiting on an outstanding ping to this peer.", "id", "addr")
	descPeerBytesSent = desc("peer_bytes_sent_total", "Bytes sent to this peer since the connection opened.", "id", "addr")
	descPeerBytesRecv = desc("peer_bytes_received_total", "Bytes received from this peer since the connection opened.", "id", "addr")
	descPeerConnTime  = desc("peer_connection_time_seconds", "Unix timestamp at which the connection to this peer opened.", "id", "addr")
	descPeerLastSend  = desc("peer_last_send_seconds", "Unix timestamp of the last message sent to this peer.", "id", "addr")
	descPeerLastRecv  = desc("peer_last_receive_seconds", "Unix timestamp of the last message received from this peer.", "id", "addr")
	descPeerStartHt   = desc("peer_starting_height", "Chain height this peer advertised when the connection opened.", "id", "addr")
	descPeerSyncedHdr = desc("peer_synced_headers", "Highest header this peer has in common with the node.", "id", "addr")
	descPeerSyncedBlk = desc("peer_synced_blocks", "Highest block this peer has in common with the node.", "id", "addr")
	descPeerFeeFilter = desc("peer_min_fee_filter_btc_per_kvb", "Minimum fee rate this peer wants transactions relayed at, in BTC/kvB.", "id", "addr")
	descPeerLastBlock = desc("peer_last_block_seconds", "Unix timestamp of the last block received from this peer; 0 if none.", "id", "addr")
	descPeerLastTx    = desc("peer_last_transaction_seconds", "Unix timestamp of the last transaction received from this peer; 0 if none.", "id", "addr")
	descPeerOffset    = desc("peer_time_offset_seconds", "Clock offset between the node and this peer.", "id", "addr")
	descPeerAddrProc  = desc("peer_addr_processed_total", "Addresses accepted from this peer's ADDR messages.", "id", "addr")
	descPeerAddrLimit = desc("peer_addr_rate_limited_total", "Addresses dropped from this peer's ADDR messages by rate limiting.", "id", "addr")
)

func (c *peerCollector) Update(ctx context.Context, ch chan<- prometheus.Metric) error {
	var errs []error

	var peers []peerInfo
	if err := c.client.Call(ctx, "getpeerinfo", nil, &peers); err != nil {
		errs = append(errs, err)
	} else {
		c.emitAggregates(ch, peers)
		if c.detail {
			c.emitDetail(ch, peers)
		}
	}

	var banned []bannedEntry
	if err := c.client.Call(ctx, "listbanned", nil, &banned); err != nil {
		errs = append(errs, err)
	} else {
		gauge(ch, descPeersBanned, float64(len(banned)))
	}

	return errors.Join(errs...)
}

func (c *peerCollector) emitAggregates(ch chan<- prometheus.Metric, peers []peerInfo) {
	byNetwork := map[string]float64{}
	bySubver := map[string]float64{}
	byConnType := map[string]float64{}
	byTransport := map[string]float64{}
	relaying := 0.0
	hbTo, hbFrom := 0.0, 0.0

	pingMin, pingMax, pingSum, pingCount := math.Inf(1), math.Inf(-1), 0.0, 0.0

	for _, p := range peers {
		byNetwork[orUnknown(p.Network)]++
		bySubver[orUnknown(p.SubVer)]++
		byConnType[orUnknown(p.ConnectionType)]++
		if p.TransportProtocol != "" {
			byTransport[p.TransportProtocol]++
		}
		if p.RelayTxes != nil && *p.RelayTxes {
			relaying++
		}
		if p.HighBandwidthTo != nil && *p.HighBandwidthTo {
			hbTo++
		}
		if p.HighBandwidthFrom != nil && *p.HighBandwidthFrom {
			hbFrom++
		}
		if p.PingTime != nil {
			pingMin = math.Min(pingMin, *p.PingTime)
			pingMax = math.Max(pingMax, *p.PingTime)
			pingSum += *p.PingTime
			pingCount++
		}
	}

	for network, n := range byNetwork {
		gauge(ch, descPeersByNetwork, n, network)
	}
	for subver, n := range bySubver {
		gauge(ch, descPeersBySubver, n, subver)
	}
	for connType, n := range byConnType {
		gauge(ch, descPeersByConnType, n, connType)
	}
	for transport, n := range byTransport {
		gauge(ch, descPeersByTransport, n, transport)
	}
	gauge(ch, descPeersRelaying, relaying)
	gauge(ch, descPeersHighBW, hbTo, "to")
	gauge(ch, descPeersHighBW, hbFrom, "from")

	if pingCount > 0 {
		gauge(ch, descPeersPing, pingMin, "min")
		gauge(ch, descPeersPing, pingMax, "max")
		gauge(ch, descPeersPing, pingSum/pingCount, "avg")
	}
}

func (c *peerCollector) emitDetail(ch chan<- prometheus.Metric, peers []peerInfo) {
	for _, p := range peers {
		id := strconv.FormatInt(p.ID, 10)
		direction := "outbound"
		if p.Inbound {
			direction = "inbound"
		}

		gauge(ch, descPeerInfo, 1, id, p.Addr, orUnknown(p.Network), direction,
			orUnknown(p.ConnectionType), p.SubVer, strconv.FormatInt(p.Version, 10))

		gaugePtr(ch, descPeerPing, p.PingTime, id, p.Addr)
		gaugePtr(ch, descPeerMinPing, p.MinPing, id, p.Addr)
		gaugePtr(ch, descPeerPingWait, p.PingWait, id, p.Addr)
		counter(ch, descPeerBytesSent, p.BytesSent, id, p.Addr)
		counter(ch, descPeerBytesRecv, p.BytesRecv, id, p.Addr)
		gauge(ch, descPeerConnTime, p.ConnTime, id, p.Addr)
		gauge(ch, descPeerLastSend, p.LastSend, id, p.Addr)
		gauge(ch, descPeerLastRecv, p.LastRecv, id, p.Addr)
		gaugePtr(ch, descPeerStartHt, p.StartingHeight, id, p.Addr)
		gaugePtr(ch, descPeerSyncedHdr, p.SyncedHeaders, id, p.Addr)
		gaugePtr(ch, descPeerSyncedBlk, p.SyncedBlocks, id, p.Addr)
		gaugePtr(ch, descPeerFeeFilter, p.MinFeeFilter, id, p.Addr)
		gaugePtr(ch, descPeerLastBlock, p.LastBlock, id, p.Addr)
		gaugePtr(ch, descPeerLastTx, p.LastTransaction, id, p.Addr)
		gaugePtr(ch, descPeerOffset, p.TimeOffset, id, p.Addr)
		if p.AddrProcessed != nil {
			counter(ch, descPeerAddrProc, *p.AddrProcessed, id, p.Addr)
		}
		if p.AddrRateLimited != nil {
			counter(ch, descPeerAddrLimit, *p.AddrRateLimited, id, p.Addr)
		}
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
