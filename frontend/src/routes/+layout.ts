import { redirect } from '@sveltejs/kit';
import { api, AuthError } from '$lib/api';
import type { AuthUser } from '$lib/api';
import { setAuth } from '$lib/stores/auth.svelte';
import type { LayoutLoad } from './$types';

export const ssr = false;

const publicRoutes = ['/login', '/setup'];

export const load: LayoutLoad = async ({ url }) => {
  const path = url.pathname;

  let auth: AuthUser | null = null;
  try {
    auth = await api.me();
    setAuth(auth);
  } catch (e) {
    if (e instanceof AuthError || (e instanceof Error && e.message.includes('401'))) {
      setAuth(null);
    }
  }

  if (!auth && !publicRoutes.includes(path)) {
    redirect(302, '/login');
  }

  return { auth };
};
