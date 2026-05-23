const BASE = '/api/v1';

export interface MediaInfo {
  codec: string;
  profile?: string;
  width: number;
  height: number;
  fps: string;
  pix_fmt?: string;
  color_space?: string;
  color_range?: string;
  bit_rate_kbps?: number;
}

export interface Stream {
  id: string;
  name: string;
  description: string;
  key_length: number;
  max_subscribers: number;
  allowed_publishers: string[];
  enabled: boolean;
  passphrase?: string;
  has_publisher: boolean;
  subscriber_count: number;
  media_info?: MediaInfo;
  latency_ms: number; // 0 = inherit global default
  created_at: string;
  updated_at: string;
}

export interface StreamPayload {
  name: string;
  description?: string;
  passphrase?: string;
  key_length?: number;
  max_subscribers?: number;
  allowed_publishers?: string[];
  enabled?: boolean;
  latency_ms?: number;
}

export interface User {
  id: string;
  username: string;
  role: string;
  created_at?: string;
}

export interface SubscriberStats {
  id: number;
  remote_addr: string;
  connected_for: number; // Go time.Duration encodes as nanoseconds
  rtt_ms: number;
  send_loss_rate_pct: number; // 0-100; % of bytes sent that were retransmits
  send_mbps: number;          // current outbound rate to this peer (incl. retrans)
  useful_mbps: number;        // unique-payload outbound to this peer (excl. retrans)
  link_capacity_mbps: number; // SRT's estimated link capacity to this peer
  pkt_sent: number;
  pkt_retrans: number;
  pkt_send_drop: number;      // packets sender abandoned (latency window exceeded)
  send_buf_ms: number;        // TSBPD buffer occupancy in ms
  pkt_flight_size: number;    // packets in flight (unacked)
}

export interface AuthUser {
  user_id: string;
  username: string;
  role: string;
  public_host?: string;
  external_port?: number;          // host port for SRT URLs in Quick Start (0 = default 9710)
  brand_name?: string;             // dashboard brand (defaults to "Bastion")
  thumbnail_refresh_ms?: number;   // dashboard thumbnail poll interval in ms
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  if (method !== 'GET' && method !== 'HEAD') {
    headers['X-Requested-With'] = 'XMLHttpRequest';
  }
  const res = await fetch(`${BASE}${path}`, {
    method,
    headers,
    credentials: 'include',
    body: body ? JSON.stringify(body) : undefined
  });
  const json = await res.json();
  if (res.status === 401) {
    throw new AuthError(json.error ?? 'session expired');
  }
  if (!res.ok) throw new Error(json.error ?? `HTTP ${res.status}`);
  return json.data as T;
}

export class AuthError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'AuthError';
  }
}

export const api = {
  setupStatus: () =>
    request<{ needs_setup: boolean }>('GET', '/auth/setup-status'),

  setup: (username: string, password: string) =>
    request<AuthUser>('POST', '/auth/setup', { username, password }),

  login: (username: string, password: string) =>
    request<AuthUser>('POST', '/auth/login', { username, password }),

  me: () => request<AuthUser>('GET', '/auth/me'),

  logout: () => request<{ status: string }>('POST', '/auth/logout'),

  listStreams: () => request<Stream[]>('GET', '/streams'),
  getStream: (name: string, reveal = false) =>
    request<Stream>('GET', `/streams/${name}${reveal ? '?reveal=true' : ''}`),
  streamSubscribers: (name: string) =>
    request<SubscriberStats[]>('GET', `/streams/${encodeURIComponent(name)}/subscribers`),
  createStream: (p: StreamPayload) => request<Stream>('POST', '/streams', p),
  updateStream: (name: string, p: Partial<StreamPayload>) =>
    request<Stream>('PUT', `/streams/${name}`, p),
  deleteStream: (name: string) => request<{ deleted: string }>('DELETE', `/streams/${name}`),

  listUsers: () => request<User[]>('GET', '/users'),
  createUser: (username: string, password: string, role: string) =>
    request<User>('POST', '/users', { username, password, role }),
  deleteUser: (id: string) => request<unknown>('DELETE', `/users/${id}`),

  streamLogs: (name: string, opts: { limit?: number; since?: number } = {}) => {
    const params = new URLSearchParams();
    if (opts.limit != null) params.set('limit', String(opts.limit));
    if (opts.since != null) params.set('since', String(opts.since));
    const qs = params.toString();
    return request<LogEntry[]>(
      'GET',
      `/streams/${encodeURIComponent(name)}/logs${qs ? '?' + qs : ''}`
    );
  },

  globalLogs: (opts: { limit?: number; since?: number } = {}) => {
    const params = new URLSearchParams();
    if (opts.limit != null) params.set('limit', String(opts.limit));
    if (opts.since != null) params.set('since', String(opts.since));
    const qs = params.toString();
    return request<LogEntry[]>('GET', `/logs${qs ? '?' + qs : ''}`);
  }
};

export interface LogEntry {
  id: number;
  ts: number; // unix nanoseconds
  level: 'debug' | 'info' | 'warn' | 'error';
  stream: string | null;
  msg: string;
  attrs: Record<string, unknown>;
}
