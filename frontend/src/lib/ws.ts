import { writable } from 'svelte/store';

export interface StreamMetrics {
  name: string;
  has_publisher: boolean;
  subscriber_count: number;
  bytes_relayed: number;
  packets_dropped: number;
  health: 'green' | 'yellow' | 'red';
  // SRT protocol stats
  rtt_ms: number;
  send_loss_rate: number;
  recv_bitrate_mbps: number;
  send_bitrate_mbps: number;
  retransmits: number;
  undecrypted: number;
}

export interface GlobalMetrics {
  active_streams: number;
  active_publishers: number;
  active_subscribers: number;
  dashboard_clients: number;
}

export interface MetricsSnapshot {
  type: 'metrics';
  timestamp: string;
  global: GlobalMetrics;
  streams: Record<string, StreamMetrics>;
}

export const metricsStore = writable<MetricsSnapshot | null>(null);

let ws: WebSocket | null = null;
let reconnectDelay = 1000;
let stopped = false;

export function connectWS(token: string) {
  stopped = false;
  const protocol = location.protocol === 'https:' ? 'wss' : 'ws';
  const url = `${protocol}://${location.host}/api/v1/ws`;

  ws = new WebSocket(`${url}?token=${encodeURIComponent(token)}`);

  ws.onopen = () => {
    reconnectDelay = 1000;
  };

  ws.onmessage = (event) => {
    try {
      const msg = JSON.parse(event.data) as MetricsSnapshot;
      if (msg.type === 'metrics') {
        metricsStore.set(msg);
      }
    } catch (_) {
      // ignore malformed messages
    }
  };

  ws.onclose = () => {
    ws = null;
    if (!stopped) {
      setTimeout(() => connectWS(token), reconnectDelay);
      reconnectDelay = Math.min(reconnectDelay * 2, 30_000);
    }
  };

  ws.onerror = () => {
    ws?.close();
  };
}

export function disconnectWS() {
  stopped = true;
  ws?.close();
  ws = null;
  metricsStore.set(null);
}
