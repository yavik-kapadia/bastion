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
}

// Collector polls relay stats periodically and broadcasts snapshots via the WebSocket hub.
type Collector struct {
	relay    RelaySnapshot
	hub      *ws.Hub
	prom     *Prom
	interval time.Duration

	// Previous bytesRelayed per stream for delta calculations.
	prevBytes map[string]uint64
}

// NewCollector creates a Collector with the given poll interval.
func NewCollector(r RelaySnapshot, hub *ws.Hub, prom *Prom, interval time.Duration) *Collector {
	return &Collector{
		relay:     r,
		hub:       hub,
		prom:      prom,
		interval:  interval,
		prevBytes: make(map[string]uint64),
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
	}
}

// healthStatus returns "green", "yellow", or "red" based on stream health.
// Without per-connection SRT stats (RTT, loss) at this layer we use
// packets-dropped as the primary signal; a future enhancement can add
// per-connection RTT once gosrt exposes Stats() on live connections.
func healthStatus(st relay.StreamStats) string {
	if !st.HasPublisher {
		return "red"
	}
	dropRate := float64(0)
	if st.BytesRelayed > 0 {
		dropRate = float64(st.PacketsDropped) / (float64(st.BytesRelayed)/1316.0 + float64(st.PacketsDropped)) * 100
	}
	switch {
	case dropRate > 5:
		return "red"
	case dropRate > 1:
		return "yellow"
	default:
		return "green"
	}
}

