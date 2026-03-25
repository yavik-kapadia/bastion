<script lang="ts">
  import { goto } from '$app/navigation';
  import { api } from '$lib/api';
  import StreamForm from '$lib/components/StreamForm.svelte';
  import type { StreamPayload } from '$lib/api';

  let loading = false;
  let error = '';

  async function handleCreate(e: CustomEvent<StreamPayload>) {
    loading = true;
    error = '';
    try {
      await api.createStream(e.detail);
      goto('/streams');
    } catch (err: unknown) {
      error = err instanceof Error ? err.message : 'Create failed';
    } finally {
      loading = false;
    }
  }
</script>

<svelte:head><title>New Stream — Bastion</title></svelte:head>

<div class="max-w-2xl">
  <div class="mb-6">
    <a href="/streams" class="text-sm text-gray-500 hover:text-gray-300">← Back to Streams</a>
    <h1 class="text-2xl font-bold mt-2">New Stream</h1>
  </div>

  <div class="card">
    <StreamForm on:submit={handleCreate} {loading} {error} />
  </div>
</div>
