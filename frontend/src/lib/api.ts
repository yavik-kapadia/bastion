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
  created_at?: string;
}

export interface AuthUser {
  user_id: string;
  username: string;
  role: string;
  public_host?: string;
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
  getStream: (name: string) => request<Stream>('GET', `/streams/${name}`),
  createStream: (p: StreamPayload) => request<Stream>('POST', '/streams', p),
  updateStream: (name: string, p: Partial<StreamPayload>) =>
    request<Stream>('PUT', `/streams/${name}`, p),
  deleteStream: (name: string) => request<{ deleted: string }>('DELETE', `/streams/${name}`),

  listUsers: () => request<User[]>('GET', '/users'),
  createUser: (username: string, password: string, role: string) =>
    request<User>('POST', '/users', { username, password, role }),
  deleteUser: (id: string) => request<unknown>('DELETE', `/users/${id}`)
};
