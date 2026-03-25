// Package metrics registers and exposes Prometheus metrics for the Bastion relay.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Prom holds all registered Prometheus metrics.
type Prom struct {
	Registry          *prometheus.Registry
	ActiveStreams      prometheus.Gauge
	ActivePublishers   prometheus.Gauge
	ActiveSubscribers  prometheus.Gauge
	BytesRelayed       *prometheus.CounterVec
	PacketsDropped     *prometheus.CounterVec
	PacketLossRate     *prometheus.GaugeVec
	RTTMs              *prometheus.GaugeVec
	BitrateInMbps      *prometheus.GaugeVec
	BitrateOutMbps     *prometheus.GaugeVec
}

// NewProm registers all Bastion metrics with a fresh Prometheus registry.
// Pass the returned Registry to promhttp.HandlerFor for the /metrics endpoint.
func NewProm() *Prom {
	reg := prometheus.NewRegistry()

	p := &Prom{
		Registry: reg,
		ActiveStreams: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "bastion_active_streams",
			Help: "Number of streams with an active publisher.",
		}),
		ActivePublishers: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "bastion_active_publishers",
			Help: "Total number of active publisher connections.",
		}),
		ActiveSubscribers: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "bastion_active_subscribers",
			Help: "Total number of active subscriber connections.",
		}),
		BytesRelayed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "bastion_bytes_relayed_total",
			Help: "Total bytes relayed per stream.",
		}, []string{"stream"}),
		PacketsDropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "bastion_packets_dropped_total",
			Help: "Total packets dropped due to slow subscribers per stream.",
		}, []string{"stream"}),
		PacketLossRate: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "bastion_packet_loss_rate",
			Help: "Packet loss percentage per stream (0-100).",
		}, []string{"stream"}),
		RTTMs: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "bastion_rtt_ms",
			Help: "Round-trip time in milliseconds per stream.",
		}, []string{"stream"}),
		BitrateInMbps: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "bastion_bitrate_in_mbps",
			Help: "Inbound bitrate from publisher in Mbps.",
		}, []string{"stream"}),
		BitrateOutMbps: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "bastion_bitrate_out_mbps",
			Help: "Outbound bitrate to subscribers in Mbps.",
		}, []string{"stream"}),
	}

	reg.MustRegister(
		p.ActiveStreams,
		p.ActivePublishers,
		p.ActiveSubscribers,
		p.BytesRelayed,
		p.PacketsDropped,
		p.PacketLossRate,
		p.RTTMs,
		p.BitrateInMbps,
		p.BitrateOutMbps,
	)
	return p
}
