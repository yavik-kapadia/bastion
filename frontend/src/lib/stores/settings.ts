import { writable, derived } from 'svelte/store';

interface Settings {
  hostUrl: string;
}

function createSettingsStore() {
  const stored = typeof localStorage !== 'undefined' ? localStorage.getItem('bastionSettings') : null;
  const initial: Settings = stored ? JSON.parse(stored) : { hostUrl: '' };

  const { subscribe, set } = writable<Settings>(initial);

  return {
    subscribe,
    setHostUrl(url: string) {
      const next = { hostUrl: url.trim() };
      if (typeof localStorage !== 'undefined') {
        localStorage.setItem('bastionSettings', JSON.stringify(next));
      }
      set(next);
    }
  };
}

export const settings = createSettingsStore();

// resolvedHost implements the three-tier host resolution:
//   1. User override (localStorage via settings store)
//   2. Server-configured public_host (sessionStorage, set at login)
//   3. window.location.hostname (auto-detect — works for direct IP and WireGuard)
export const resolvedHost = derived(settings, ($settings) => {
  if ($settings.hostUrl) return $settings.hostUrl;
  if (typeof sessionStorage !== 'undefined') {
    const serverHost = sessionStorage.getItem('bastionPublicHost');
    if (serverHost) return serverHost;
  }
  if (typeof window !== 'undefined') return window.location.hostname;
  return '';
});
