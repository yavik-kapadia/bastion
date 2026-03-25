const BASE = '/api/v1';

export interface Stream {
  id: string;
  name: string;
  description: string;
  key_length: number;
  max_subscribers: number;
  allowed_publishers: string[];
  enabled: boolean;
  has_publisher: boolean;
  subscriber_count: number;
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
}

export interface User {
  id: string;
  username: string;
  role: string;
}

export interface AuthResponse {
  token: string;
  user_id: string;
  username: string;
  role: string;
}

function token(): string {
  return localStorage.getItem('token') ?? '';
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token()}`
    },
    body: body ? JSON.stringify(body) : undefined
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error ?? `HTTP ${res.status}`);
  return json.data as T;
}

export const api = {
  login: (username: string, password: string) =>
    request<AuthResponse>('POST', '/auth/login', { username, password }),

  listStreams: () => request<Stream[]>('GET', '/streams'),
  getStream: (name: string) => request<Stream>('GET', `/streams/${name}`),
  createStream: (p: StreamPayload) => request<Stream>('POST', '/streams', p),
  updateStream: (name: string, p: Partial<StreamPayload>) =>
    request<Stream>('PUT', `/streams/${name}`, p),
  deleteStream: (name: string) => request<{ deleted: string }>('DELETE', `/streams/${name}`),

  listUsers: () => request<User[]>('GET', '/users'),
  createUser: (username: string, password: string, role: string) =>
    request<User>('POST', '/users', { username, password, role }),
  deleteUser: (id: string) => request<unknown>('DELETE', `/users/${id}`),

  createAPIKey: (name: string) =>
    request<{ key: string; note: string }>('POST', '/auth/api-keys', { name }),

  globalMetrics: () =>
    request<{ active_streams: number; active_publishers: number; active_subscribers: number }>(
      'GET',
      '/metrics/global'
    )
};
