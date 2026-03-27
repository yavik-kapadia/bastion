import { getAuth } from './auth.svelte';

let hostUrl = $state(
  typeof localStorage !== 'undefined'
    ? JSON.parse(localStorage.getItem('bastionSettings') ?? '{}').hostUrl ?? ''
    : ''
);

export function getHostUrl(): string {
  return hostUrl;
}

export function setHostUrl(url: string) {
  hostUrl = url.trim();
  if (typeof localStorage !== 'undefined') {
    localStorage.setItem('bastionSettings', JSON.stringify({ hostUrl }));
  }
}

// resolvedHost implements three-tier host resolution:
//   1. User override (localStorage)
//   2. Server-configured public_host (from /auth/me via auth store)
//   3. window.location.hostname (auto-detect)
export function resolvedHost(): string {
  if (hostUrl) return hostUrl;
  const auth = getAuth();
  if (auth?.public_host) return auth.public_host;
  if (typeof window !== 'undefined') return window.location.hostname;
  return '';
}
