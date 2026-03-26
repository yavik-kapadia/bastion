import { writable, derived } from 'svelte/store';

export interface AuthState {
  token: string;
  userId: string;
  username: string;
  role: string;
}

function createAuthStore() {
  const stored = typeof localStorage !== 'undefined' ? localStorage.getItem('auth') : null;
  const initial: AuthState | null = stored ? JSON.parse(stored) : null;

  const { subscribe, set, update } = writable<AuthState | null>(initial);

  return {
    subscribe,
    login(state: AuthState) {
      localStorage.setItem('auth', JSON.stringify(state));
      localStorage.setItem('token', state.token);
      set(state);
    },
    logout() {
      localStorage.removeItem('auth');
      localStorage.removeItem('token');
      set(null);
    }
  };
}

export const auth = createAuthStore();
export const isLoggedIn = derived(auth, ($auth) => $auth !== null);
export const isAdmin = derived(auth, ($auth) => $auth?.role === 'admin');
export const isManager = derived(auth, ($auth) => $auth?.role === 'admin' || $auth?.role === 'manager');
