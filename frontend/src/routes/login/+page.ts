import { redirect } from '@sveltejs/kit';
import { api } from '$lib/api';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ parent }) => {
  const { auth } = await parent();
  if (auth) redirect(302, '/');

  try {
    const { needs_setup } = await api.setupStatus();
    if (needs_setup) redirect(302, '/setup');
  } catch {
    // If setup-status fails, show login form anyway
  }

  return {};
};
