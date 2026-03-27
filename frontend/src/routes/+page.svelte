<script lang="ts">
  import { metricsStore } from '$lib/ws';
  import { api } from '$lib/api';
  import StreamCard from '$lib/components/StreamCard.svelte';
  import GlobalStats from '$lib/components/GlobalStats.svelte';
  import type { Stream } from '$lib/api';

  let { data } = $props();
  let streams: Stream[] = $state(data.streams);

  // Reactively update stream list when metrics arrive (add new streams dynamically).
  $effect(() => {
    if ($metricsStore) {
      const knownNames = new Set(streams.map((s) => s.name));
      const metricNames = Object.keys($metricsStore.streams);
      if (metricNames.some((n) => !knownNames.has(n))) {
        api.listStreams().then((s) => { streams = s; }).catch(() => {});
      }
    }
  });
</script>

<svelte:head>
  <title>Dashboard — Bastion</title>
</svelte:head>

<div class="space-y-8">
  <div>
    <h1 class="text-2xl font-bold text-gray-100">Dashboard</h1>
    <p class="text-gray-500 text-sm mt-1">Live overview of all SRT relay streams</p>
  </div>

  <GlobalStats metrics={$metricsStore?.global ?? null} />

  {#if streams.length === 0}
    <div class="card text-center py-12">
      <p class="text-gray-500">No streams configured yet.</p>
      <a href="/streams/new" class="btn-primary inline-block mt-4">Create your first stream</a>
    </div>
  {:else}
    <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
      {#each streams as stream (stream.id)}
        <StreamCard
          {stream}
          metrics={$metricsStore?.streams[stream.name]}
        />
      {/each}
    </div>
  {/if}
</div>
