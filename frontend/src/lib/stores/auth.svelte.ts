import type { AuthUser } from '$lib/api';

let currentUser = $state<AuthUser | null>(null);

export function getAuth(): AuthUser | null {
  return currentUser;
}

export function setAuth(user: AuthUser | null) {
  currentUser = user;
}

export function clearAuth() {
  currentUser = null;
}

export function isLoggedIn(): boolean {
  return currentUser !== null;
}

export function isAdmin(): boolean {
  return currentUser?.role === 'admin';
}

export function isManager(): boolean {
  return currentUser?.role === 'admin' || currentUser?.role === 'manager';
}
