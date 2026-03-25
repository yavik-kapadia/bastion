<script lang="ts">
  import type { GlobalMetrics } from '$lib/ws';

  export let metrics: GlobalMetrics | null = null;
</script>

<div class="grid grid-cols-2 sm:grid-cols-4 gap-4">
  {#each [
    { label: 'Active Streams', value: metrics?.active_streams ?? '—' },
    { label: 'Publishers', value: metrics?.active_publishers ?? '—' },
    { label: 'Subscribers', value: metrics?.active_subscribers ?? '—' },
    { label: 'Dashboard Users', value: metrics?.dashboard_clients ?? '—' }
  ] as stat}
    <div class="card text-center">
      <div class="text-2xl font-bold text-sky-400">{stat.value}</div>
      <div class="text-xs text-gray-500 mt-1">{stat.label}</div>
    </div>
  {/each}
</div>
