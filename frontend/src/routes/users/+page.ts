import { redirect } from '@sveltejs/kit';
import { api } from '$lib/api';
import { isAdmin } from '$lib/stores/auth.svelte';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ parent }) => {
  const { auth } = await parent();
  if (!auth || auth.role !== 'admin') redirect(302, '/');

  const users = await api.listUsers();
  return { users };
};
