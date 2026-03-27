import { api } from '$lib/api';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params }) => {
  const stream = await api.getStream(params.name);
  return { stream };
};
