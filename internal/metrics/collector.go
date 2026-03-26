package metrics

import (
	"context"
	"encoding/json"
	"time"

	"github.com/yavik14/bastion/internal/relay"
	"github.com/yavik14/bastion/internal/ws"
)

// RelaySnapshot is the relay interface subset needed by the collector.
type RelaySnapshot interface {
	ActiveStreams() map[string]relay.StreamStats
}

// MetricsSnapshot is the JSON payload pushed to WebSocket clients each tick.
type MetricsSnapshot struct {
	Type      string                    `json:"type"`
	Timestamp time.Time                 `json:"timestamp"`
	Global    GlobalMetrics             `json:"global"`
	Streams   map[string]StreamMetrics  `json:"streams"`
}

// GlobalMetrics aggregates server-wide statistics.
type GlobalMetrics struct {
	ActiveStreams      int `json:"active_streams"`
	ActivePublishers   int `json:"active_publishers"`
	ActiveSubscribers  int `json:"active_subscribers"`
	DashboardClients  int `json:"dashboard_clients"`
}

// StreamMetrics holds per-stream statistics for one snapshot tick.
type StreamMetrics struct {
	Name            string  `json:"name"`
	HasPublisher    bool    `json:"has_publisher"`
	SubscriberCount int     `json:"subscriber_count"`
	BytesRelayed    uint64  `json:"bytes_relayed"`
	PacketsDropped  uint64  `json:"packets_dropped"`
	Health          string  `json:"health"` // "green", "yellow", "red"
	// SRT protocol-level stats from conn.Stats()
	RttMs           float64 `json:"rtt_ms"`
	SendLossRate    float64 `json:"send_loss_rate"`
	RecvBitrateMbps float64 `json:"recv_bitrate_mbps"`
	SendBitrateMbps float64 `json:"send_bitrate_mbps"`
	Retransmits     uint64  `json:"retransmits"`
	Undecrypted     uint64  `json:"undecrypted"`
}

// Collector polls relay stats periodically and broadcasts snapshots via the WebSocket hub.
type Collector struct {
	relay    RelaySnapshot
	hub      *ws.Hub
	prom     *Prom
	interval time.Duration

	// Previous cumulative values per stream for delta calculations.
	prevBytes     map[string]uint64
	prevRetrans   map[string]uint64
	prevUndecrypt map[string]uint64
}

// NewCollector creates a Collector with the given poll interval.
func NewCollector(r RelaySnapshot, hub *ws.Hub, prom *Prom, interval time.Duration) *Collector {
	return &Collector{
		relay:         r,
		hub:           hub,
		prom:          prom,
		interval:      interval,
		prevBytes:     make(map[string]uint64),
		prevRetrans:   make(map[string]uint64),
		prevUndecrypt: make(map[string]uint64),
	}
}

// Start runs the collection loop until ctx is cancelled.
func (c *Collector) Start(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			snap := c.collect()
			c.updatePrometheus(snap)
			if data, err := json.Marshal(snap); err == nil {
				c.hub.Broadcast(data)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (c *Collector) collect() MetricsSnapshot {
	active := c.relay.ActiveStreams()

	snap := MetricsSnapshot{
		Type:      "metrics",
		Timestamp: time.Now().UTC(),
		Streams:   make(map[string]StreamMetrics, len(active)),
	}

	totalPubs := 0
	totalSubs := 0

	for name, st := range active {
		if st.HasPublisher {
			totalPubs++
		}
		totalSubs += st.SubscriberCount

		health := healthStatus(st)

		snap.Streams[name] = StreamMetrics{
			Name:            name,
			HasPublisher:    st.HasPublisher,
			SubscriberCount: st.SubscriberCount,
			BytesRelayed:    st.BytesRelayed,
			PacketsDropped:  st.PacketsDropped,
			Health:          health,
			RttMs:           st.SRT.MsRTT,
			SendLossRate:    st.SRT.SendLossRate,
			RecvBitrateMbps: st.SRT.RecvBitrateMbps,
			SendBitrateMbps: st.SRT.SendBitrateMbps,
			Retransmits:     st.SRT.PktRetrans,
			Undecrypted:     st.SRT.PktUndecrypt,
		}
	}

	snap.Global = GlobalMetrics{
		ActiveStreams:     len(active),
		ActivePublishers:  totalPubs,
		ActiveSubscribers: totalSubs,
		DashboardClients:  c.hub.ClientCount(),
	}
	return snap
}

func (c *Collector) updatePrometheus(snap MetricsSnapshot) {
	c.prom.ActiveStreams.Set(float64(snap.Global.ActiveStreams))
	c.prom.ActivePublishers.Set(float64(snap.Global.ActivePublishers))
	c.prom.ActiveSubscribers.Set(float64(snap.Global.ActiveSubscribers))

	for name, sm := range snap.Streams {
		prev := c.prevBytes[name]
		delta := sm.BytesRelayed - prev
		if delta > 0 {
			c.prom.BytesRelayed.WithLabelValues(name).Add(float64(delta))
		}
		c.prevBytes[name] = sm.BytesRelayed

		if sm.PacketsDropped > 0 {
			c.prom.PacketsDropped.WithLabelValues(name).Add(float64(sm.PacketsDropped))
		}

		// SRT protocol metrics — only meaningful when publisher is active.
		if sm.HasPublisher {
			c.prom.RTTMs.WithLabelValues(name).Set(sm.RttMs)
			c.prom.PacketLossRate.WithLabelValues(name).Set(sm.SendLossRate)
			c.prom.BitrateInMbps.WithLabelValues(name).Set(sm.RecvBitrateMbps)
			c.prom.BitrateOutMbps.WithLabelValues(name).Set(sm.SendBitrateMbps)
		}

		prevRetrans := c.prevRetrans[name]
		if sm.Retransmits > prevRetrans {
			c.prom.Retransmits.WithLabelValues(name).Add(float64(sm.Retransmits - prevRetrans))
		}
		c.prevRetrans[name] = sm.Retransmits

		prevUndecrypt := c.prevUndecrypt[name]
		if sm.Undecrypted > prevUndecrypt {
			c.prom.Undecrypted.WithLabelValues(name).Add(float64(sm.Undecrypted - prevUndecrypt))
		}
		c.prevUndecrypt[name] = sm.Undecrypted
	}
}

// healthStatus returns "green", "yellow", or "red" based on real SRT protocol
// metrics (RTT and send-path loss rate) from conn.Stats().
func healthStatus(st relay.StreamStats) string {
	if !st.HasPublisher {
		return "red"
	}
	rtt := st.SRT.MsRTT
	loss := st.SRT.SendLossRate
	switch {
	case rtt > 200 || loss > 1:
		return "red"
	case rtt > 50 || loss > 0.1:
		return "yellow"
	default:
		return "green"
	}
}

