<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '$lib/api';
  import { metricsStore } from '$lib/ws';
  import StreamCard from '$lib/components/StreamCard.svelte';
  import type { Stream } from '$lib/api';

  let streams: Stream[] = [];
  let loadError = '';

  onMount(async () => {
    try {
      streams = await api.listStreams();
    } catch (e: unknown) {
      loadError = e instanceof Error ? e.message : 'Failed to load';
    }
  });
</script>

<svelte:head><title>Streams — Bastion</title></svelte:head>

<div class="space-y-6">
  <div class="flex items-center justify-between">
    <div>
      <h1 class="text-2xl font-bold">Streams</h1>
      <p class="text-gray-500 text-sm mt-1">Manage SRT relay stream configurations</p>
    </div>
    <a href="/streams/new" class="btn-primary">+ New Stream</a>
  </div>

  {#if loadError}
    <div class="card border-red-800 text-red-300">{loadError}</div>
  {:else if streams.length === 0}
    <div class="card text-center py-12">
      <p class="text-gray-500">No streams yet.</p>
      <a href="/streams/new" class="btn-primary inline-block mt-4">Create Stream</a>
    </div>
  {:else}
    <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
      {#each streams as stream (stream.id)}
        <StreamCard {stream} metrics={$metricsStore?.streams[stream.name]} />
      {/each}
    </div>
  {/if}
</div>
