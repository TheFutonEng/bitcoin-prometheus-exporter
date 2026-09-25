package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TheFutonEng/bitcoin-prometheus-exporter/internal/rpc"
)

func init() {
	register("wallet", "Balances and state of loaded wallets (listwallets, getwalletinfo, getbalances).", false,
		func(c *rpc.Client, cfg Config) (Collector, error) {
			return &walletCollector{client: c, only: cfg.Wallets}, nil
		})
}

// scanning decodes getwalletinfo's scanning field, which is either the literal
// false or an object describing a rescan in progress.
type scanning struct {
	Active   bool
	Duration float64
	Progress float64
}

func (s *scanning) UnmarshalJSON(b []byte) error {
	var flag bool
	if err := json.Unmarshal(b, &flag); err == nil {
		*s = scanning{Active: flag}
		return nil
	}
	var obj struct {
		Duration float64 `json:"duration"`
		Progress float64 `json:"progress"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	*s = scanning{Active: true, Duration: obj.Duration, Progress: obj.Progress}
	return nil
}

type walletInfo struct {
	WalletName         string   `json:"walletname"`
	WalletVersion      int64    `json:"walletversion"`
	Format             string   `json:"format"`
	TxCount            float64  `json:"txcount"`
	KeypoolOldest      *float64 `json:"keypoololdest"`
	KeypoolSize        *float64 `json:"keypoolsize"`
	KeypoolSizeHD      *float64 `json:"keypoolsize_hd_internal"`
	UnlockedUntil      *float64 `json:"unlocked_until"`
	PayTxFee           float64  `json:"paytxfee"`
	PrivateKeysEnabled *bool    `json:"private_keys_enabled"`
	AvoidReuse         *bool    `json:"avoid_reuse"`
	Descriptors        *bool    `json:"descriptors"`
	Blank              *bool    `json:"blank"`
	Birthtime          *float64 `json:"birthtime"`
	Scanning           scanning `json:"scanning"`
	LastProcessedBlock *struct {
		Height float64 `json:"height"`
	} `json:"lastprocessedblock"`
}

type balanceSet struct {
	Trusted          *float64 `json:"trusted"`
	UntrustedPending *float64 `json:"untrusted_pending"`
	Immature         *float64 `json:"immature"`
	Used             *float64 `json:"used"`
}

type balances struct {
	Mine      balanceSet  `json:"mine"`
	WatchOnly *balanceSet `json:"watchonly"`
}

type walletCollector struct {
	client *rpc.Client
	only   []string
}

var (
	descWalletsLoaded  = desc("wallets_loaded", "Number of wallets currently loaded by the node.")
	descWalletInfo     = desc("wallet_info", "Always 1, labelled with the descriptive attributes of a wallet.", "wallet", "version", "format")
	descWalletBalance  = desc("wallet_balance_btc", "Wallet balance in BTC, split by ownership and spendability.", "wallet", "owner", "category")
	descWalletTxCount  = desc("wallet_txs", "Number of transactions in the wallet.", "wallet")
	descWalletKeypool  = desc("wallet_keypool_size", "Pre-generated keys remaining in the wallet keypool.", "wallet", "chain")
	descWalletKeyOld   = desc("wallet_keypool_oldest_seconds", "Unix timestamp of the oldest key in the keypool.", "wallet")
	descWalletUnlocked = desc("wallet_unlocked_until_seconds", "Unix timestamp until which an encrypted wallet stays unlocked; 0 means locked.", "wallet")
	descWalletPayFee   = desc("wallet_pay_tx_fee_btc_per_kvb", "Configured -paytxfee for this wallet, in BTC/kvB.", "wallet")
	descWalletPrivKeys = desc("wallet_private_keys_enabled", "1 if the wallet holds private keys.", "wallet")
	descWalletDescr    = desc("wallet_descriptors", "1 if the wallet uses descriptors rather than legacy keys.", "wallet")
	descWalletScanning = desc("wallet_scanning", "1 while the wallet is rescanning the chain.", "wallet")
	descWalletScanDur  = desc("wallet_scanning_duration_seconds", "How long the in-progress rescan has been running.", "wallet")
	descWalletScanProg = desc("wallet_scanning_progress", "Progress of the in-progress rescan, 0 to 1.", "wallet")
	descWalletBirth    = desc("wallet_birthtime_seconds", "Unix timestamp of the earliest transaction the wallet needs to scan from.", "wallet")
	descWalletHeight   = desc("wallet_last_processed_block_height", "Height of the last block the wallet processed.", "wallet")
)

func (c *walletCollector) Update(ctx context.Context, ch chan<- prometheus.Metric) error {
	var loaded []string
	if err := c.client.Call(ctx, "listwallets", nil, &loaded); err != nil {
		// A node started with -disablewallet has no wallet RPCs at all, which
		// is a configuration choice rather than a failure.
		if rpc.Code(err) == rpc.ErrMethodNotFound {
			gauge(ch, descWalletsLoaded, 0)
			return nil
		}
		return err
	}

	wallets := loaded
	if len(c.only) > 0 {
		wallets = nil
		for _, name := range c.only {
			if slices.Contains(loaded, name) {
				wallets = append(wallets, name)
			}
		}
	}
	gauge(ch, descWalletsLoaded, float64(len(loaded)))

	var errs []error
	for _, name := range wallets {
		if err := c.collectWallet(ctx, ch, name); err != nil {
			errs = append(errs, fmt.Errorf("wallet %q: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func (c *walletCollector) collectWallet(ctx context.Context, ch chan<- prometheus.Metric, name string) error {
	var errs []error

	var info walletInfo
	if err := c.client.CallWallet(ctx, name, "getwalletinfo", nil, &info); err != nil {
		errs = append(errs, err)
	} else {
		gauge(ch, descWalletInfo, 1, name, strconv.FormatInt(info.WalletVersion, 10), orUnknown(info.Format))
		gauge(ch, descWalletTxCount, info.TxCount, name)
		gaugePtr(ch, descWalletKeypool, info.KeypoolSize, name, "external")
		gaugePtr(ch, descWalletKeypool, info.KeypoolSizeHD, name, "internal")
		gaugePtr(ch, descWalletKeyOld, info.KeypoolOldest, name)
		gaugePtr(ch, descWalletUnlocked, info.UnlockedUntil, name)
		gauge(ch, descWalletPayFee, info.PayTxFee, name)
		if v, ok := boolPtrToFloat(info.PrivateKeysEnabled); ok {
			gauge(ch, descWalletPrivKeys, v, name)
		}
		if v, ok := boolPtrToFloat(info.Descriptors); ok {
			gauge(ch, descWalletDescr, v, name)
		}
		gauge(ch, descWalletScanning, boolToFloat(info.Scanning.Active), name)
		if info.Scanning.Active {
			gauge(ch, descWalletScanDur, info.Scanning.Duration, name)
			gauge(ch, descWalletScanProg, info.Scanning.Progress, name)
		}
		gaugePtr(ch, descWalletBirth, info.Birthtime, name)
		if info.LastProcessedBlock != nil {
			gauge(ch, descWalletHeight, info.LastProcessedBlock.Height, name)
		}
	}

	var bal balances
	if err := c.client.CallWallet(ctx, name, "getbalances", nil, &bal); err != nil {
		errs = append(errs, err)
	} else {
		emitBalances(ch, name, "mine", bal.Mine)
		if bal.WatchOnly != nil {
			emitBalances(ch, name, "watchonly", *bal.WatchOnly)
		}
	}

	return errors.Join(errs...)
}

func emitBalances(ch chan<- prometheus.Metric, wallet, owner string, set balanceSet) {
	gaugePtr(ch, descWalletBalance, set.Trusted, wallet, owner, "trusted")
	gaugePtr(ch, descWalletBalance, set.UntrustedPending, wallet, owner, "untrusted_pending")
	gaugePtr(ch, descWalletBalance, set.Immature, wallet, owner, "immature")
	gaugePtr(ch, descWalletBalance, set.Used, wallet, owner, "used")
}
