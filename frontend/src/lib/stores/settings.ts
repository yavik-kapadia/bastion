import { writable } from 'svelte/store';

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
