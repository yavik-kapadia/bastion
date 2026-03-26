<script lang="ts">
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { api } from '$lib/api';
  import { metricsStore } from '$lib/ws';
  import { isManager } from '$lib/stores/auth';
  import HealthBadge from '$lib/components/HealthBadge.svelte';
  import StreamForm from '$lib/components/StreamForm.svelte';
  import type { Stream, StreamPayload } from '$lib/api';

  let stream: Stream | null = null;
  let loadError = '';
  let editing = false;
  let updateLoading = false;
  let updateError = '';
  let deleteLoading = false;

  $: name = $page.params.name;
  $: metrics = $metricsStore?.streams[name];
  $: health = metrics?.health ?? (stream?.has_publisher ? 'yellow' : 'red');

  onMount(async () => {
    try {
      stream = await api.getStream(name);
    } catch (e: unknown) {
      loadError = e instanceof Error ? e.message : 'Not found';
    }
  });

  async function handleUpdate(e: CustomEvent<StreamPayload>) {
    updateLoading = true;
    updateError = '';
    try {
      stream = await api.updateStream(name, e.detail);
      editing = false;
    } catch (err: unknown) {
      updateError = err instanceof Error ? err.message : 'Update failed';
    } finally {
      updateLoading = false;
    }
  }

  async function handleDelete() {
    if (!confirm(`Delete stream "${name}"?`)) return;
    deleteLoading = true;
    try {
      await api.deleteStream(name);
      goto('/streams');
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : 'Delete failed');
    } finally {
      deleteLoading = false;
    }
  }

  function formatBytes(bytes: number): string {
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
    return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`;
  }
</script>

<svelte:head><title>{name} — Bastion</title></svelte:head>

{#if loadError}
  <div class="card border-red-800 text-red-300">{loadError}</div>
{:else if !stream}
  <div class="text-gray-500">Loading…</div>
{:else}
  <div class="space-y-6 max-w-4xl">
    <!-- Header -->
    <div class="flex items-start justify-between">
      <div>
        <a href="/streams" class="text-sm text-gray-500 hover:text-gray-300">← Streams</a>
        <div class="flex items-center gap-3 mt-2">
          <h1 class="text-2xl font-bold">{stream.name}</h1>
          <HealthBadge {health} />
        </div>
        {#if stream.description}
          <p class="text-gray-500 text-sm mt-1">{stream.description}</p>
        {/if}
      </div>
      {#if $isManager}
        <div class="flex gap-2">
          <button class="btn-ghost" on:click={() => { editing = !editing; updateError = ''; }}>
            {editing ? 'Cancel' : 'Edit'}
          </button>
          <button class="btn-danger" on:click={handleDelete} disabled={deleteLoading}>
            {deleteLoading ? 'Deleting…' : 'Delete'}
          </button>
        </div>
      {/if}
    </div>

    <!-- Live metrics -->
    {#if metrics}
      <div class="grid grid-cols-2 sm:grid-cols-4 gap-4">
        <div class="card text-center">
          <div class="text-2xl font-bold text-sky-400">{metrics.subscriber_count}</div>
          <div class="text-xs text-gray-500 mt-1">Subscribers</div>
        </div>
        <div class="card text-center">
          <div class="text-2xl font-bold text-sky-400">{formatBytes(metrics.bytes_relayed)}</div>
          <div class="text-xs text-gray-500 mt-1">Bytes Relayed</div>
        </div>
        <div class="card text-center">
          <div class="text-2xl font-bold {metrics.packets_dropped > 0 ? 'text-yellow-400' : 'text-sky-400'}">
            {metrics.packets_dropped}
          </div>
          <div class="text-xs text-gray-500 mt-1">Pkts Dropped</div>
        </div>
        <div class="card text-center">
          <div class="text-2xl font-bold text-sky-400">
            {stream.key_length > 0 ? `AES-${stream.key_length * 8}` : 'None'}
          </div>
          <div class="text-xs text-gray-500 mt-1">Encryption</div>
        </div>
      </div>

      <!-- SRT protocol metrics (visible when publisher is active) -->
      {#if metrics.has_publisher}
        <div class="grid grid-cols-2 sm:grid-cols-4 gap-4">
          <div class="card text-center">
            <div class="text-2xl font-bold {metrics.rtt_ms > 200 ? 'text-red-400' : metrics.rtt_ms > 50 ? 'text-yellow-400' : 'text-sky-400'}">
              {metrics.rtt_ms.toFixed(1)} ms
            </div>
            <div class="text-xs text-gray-500 mt-1">RTT</div>
          </div>
          <div class="card text-center">
            <div class="text-2xl font-bold {metrics.send_loss_rate > 1 ? 'text-red-400' : metrics.send_loss_rate > 0.1 ? 'text-yellow-400' : 'text-sky-400'}">
              {metrics.send_loss_rate.toFixed(2)}%
            </div>
            <div class="text-xs text-gray-500 mt-1">Loss Rate</div>
          </div>
          <div class="card text-center">
            <div class="text-2xl font-bold text-sky-400">
              {metrics.recv_bitrate_mbps.toFixed(2)} Mbps
            </div>
            <div class="text-xs text-gray-500 mt-1">Inbound</div>
          </div>
          <div class="card text-center">
            <div class="text-2xl font-bold text-sky-400">
              {metrics.send_bitrate_mbps.toFixed(2)} Mbps
            </div>
            <div class="text-xs text-gray-500 mt-1">Outbound</div>
          </div>
        </div>
        {#if metrics.retransmits > 0 || metrics.undecrypted > 0}
          <div class="grid grid-cols-2 gap-4">
            <div class="card text-center">
              <div class="text-2xl font-bold {metrics.retransmits > 0 ? 'text-yellow-400' : 'text-sky-400'}">
                {metrics.retransmits}
              </div>
              <div class="text-xs text-gray-500 mt-1">Retransmits</div>
            </div>
            <div class="card text-center">
              <div class="text-2xl font-bold {metrics.undecrypted > 0 ? 'text-red-400' : 'text-sky-400'}">
                {metrics.undecrypted}
              </div>
              <div class="text-xs text-gray-500 mt-1">Undecrypted</div>
            </div>
          </div>
        {/if}
      {/if}
    {/if}

    <!-- Stream config -->
    <div class="card">
      <h2 class="font-semibold mb-4">Configuration</h2>
      {#if editing && $isManager}
        <StreamForm
          initial={stream}
          submitLabel="Save Changes"
          loading={updateLoading}
          error={updateError}
          on:submit={handleUpdate}
        />
      {:else}
        <dl class="grid grid-cols-2 gap-x-6 gap-y-3 text-sm">
          <div>
            <dt class="text-gray-500">Status</dt>
            <dd>{stream.enabled ? 'Enabled' : 'Disabled'}</dd>
          </div>
          <div>
            <dt class="text-gray-500">Max Subscribers</dt>
            <dd>{stream.max_subscribers === 0 ? 'Unlimited' : stream.max_subscribers}</dd>
          </div>
          <div>
            <dt class="text-gray-500">Encryption</dt>
            <dd>{stream.key_length > 0 ? `AES-${stream.key_length * 8}` : 'None'}</dd>
          </div>
          <div>
            <dt class="text-gray-500">Allowed Publishers</dt>
            <dd>
              {stream.allowed_publishers?.length
                ? stream.allowed_publishers.join(', ')
                : 'Any'}
            </dd>
          </div>
          <div>
            <dt class="text-gray-500">Created</dt>
            <dd>{new Date(stream.created_at).toLocaleString()}</dd>
          </div>
          <div>
            <dt class="text-gray-500">Updated</dt>
            <dd>{new Date(stream.updated_at).toLocaleString()}</dd>
          </div>
        </dl>

        <!-- Publish/subscribe commands -->
        <div class="mt-6 border-t border-gray-800 pt-4">
          <h3 class="text-sm font-medium text-gray-400 mb-3">Quick Start</h3>
          <div class="space-y-2">
            <div>
              <div class="text-xs text-gray-500 mb-1">Publish</div>
              <code class="block bg-gray-950 rounded px-3 py-2 text-xs text-green-400 font-mono overflow-x-auto">
                ffmpeg -re -i input.ts -c copy -f mpegts "srt://&lt;host&gt;:9710?streamid=#!::m=publish,r={stream.name}{stream.key_length > 0 ? '&passphrase=<pass>' : ''}"
              </code>
            </div>
            <div>
              <div class="text-xs text-gray-500 mb-1">Subscribe</div>
              <code class="block bg-gray-950 rounded px-3 py-2 text-xs text-blue-400 font-mono overflow-x-auto">
                ffplay "srt://&lt;host&gt;:9710?streamid=#!::m=request,r={stream.name}{stream.key_length > 0 ? '&passphrase=<pass>' : ''}"
              </code>
            </div>
          </div>
        </div>
      {/if}
    </div>
  </div>
{/if}
