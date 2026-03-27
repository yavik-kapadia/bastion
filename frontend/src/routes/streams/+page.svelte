<script lang="ts">
  import { metricsStore } from '$lib/ws';
  import { isManager } from '$lib/stores/auth.svelte';
  import StreamCard from '$lib/components/StreamCard.svelte';

  let { data } = $props();
</script>

<svelte:head><title>Streams — Bastion</title></svelte:head>

<div class="space-y-6">
  <div class="flex items-center justify-between">
    <div>
      <h1 class="text-2xl font-bold">Streams</h1>
      <p class="text-gray-500 text-sm mt-1">Manage SRT relay stream configurations</p>
    </div>
    {#if isManager()}<a href="/streams/new" class="btn-primary">+ New Stream</a>{/if}
  </div>

  {#if data.streams.length === 0}
    <div class="card text-center py-12">
      <p class="text-gray-500">No streams configured yet.</p>
      {#if isManager()}<a href="/streams/new" class="btn-primary inline-block mt-4">Create Stream</a>{/if}
    </div>
  {:else}
    <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
      {#each data.streams as stream (stream.id)}
        <StreamCard {stream} metrics={$metricsStore?.streams[stream.name]} />
      {/each}
    </div>
  {/if}
</div>
