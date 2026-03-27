import { api } from '$lib/api';
import type { PageLoad } from './$types';

export const load: PageLoad = async () => {
  const streams = await api.listStreams();
  return { streams };
};
