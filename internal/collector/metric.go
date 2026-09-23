package collector

import (
	"github.com/prometheus/client_golang/prometheus"
)

// allDescs accumulates every descriptor the collectors declare. Collectors
// build their descriptors in package variables, so this is complete before the
// first scrape and lets Exporter.Describe report them without each collector
// having to maintain its own list.
var allDescs []*prometheus.Desc

// desc builds a fully qualified metric descriptor in the exporter namespace and
// records it for Exporter.Describe.
func desc(name, help string, labels ...string) *prometheus.Desc {
	d := prometheus.NewDesc(prometheus.BuildFQName(Namespace, "", name), help, labels, nil)
	allDescs = append(allDescs, d)
	return d
}

// gauge sends a gauge sample for d.
func gauge(ch chan<- prometheus.Metric, d *prometheus.Desc, v float64, labels ...string) {
	ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
}

// counter sends a monotonic counter sample for d.
func counter(ch chan<- prometheus.Metric, d *prometheus.Desc, v float64, labels ...string) {
	ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v, labels...)
}

// gaugePtr sends a sample only when the node actually reported the field,
// which keeps optional and version-dependent RPC fields from showing up as 0.
func gaugePtr[T ~float64 | ~int64 | ~int](ch chan<- prometheus.Metric, d *prometheus.Desc, v *T, labels ...string) {
	if v == nil {
		return
	}
	gauge(ch, d, float64(*v), labels...)
}

func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func boolPtrToFloat(b *bool) (float64, bool) {
	if b == nil {
		return 0, false
	}
	return boolToFloat(*b), true
}

// satPerVByte converts a BTC/kvB fee rate, the unit bitcoind reports, into the
// sat/vB unit everyone actually reads fees in. Dividing before scaling keeps
// the common whole-number rates exact in float64.
func satPerVByte(btcPerKvB float64) float64 {
	return btcPerKvB / 1000 * 1e8
}
