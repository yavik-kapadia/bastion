<script lang="ts">
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { onMount, onDestroy } from 'svelte';
  import { api } from '$lib/api';
  import { metricsStore } from '$lib/ws';
  import { isManager } from '$lib/stores/auth';
  import { settings } from '$lib/stores/settings';
  import HealthBadge from '$lib/components/HealthBadge.svelte';
  import StreamForm from '$lib/components/StreamForm.svelte';
  import type { Stream, StreamPayload } from '$lib/api';

  let stream: Stream | null = null;
  let loadError = '';
  let editing = false;
  let updateLoading = false;
  let updateError = '';
  let deleteLoading = false;

  // Host URL setting
  let hostInput = $settings.hostUrl;

  // Thumbnail
  let thumbnailSrc = '';
  let thumbnailError = false;
  let thumbnailInterval: ReturnType<typeof setInterval>;

  // Copy feedback
  let copiedPublish = false;
  let copiedSubscribe = false;

  $: name = $page.params.name;
  $: metrics = $metricsStore?.streams[name];
  $: health = metrics?.health ?? (stream?.has_publisher ? 'yellow' : 'red');
  $: hasPublisher = metrics?.has_publisher ?? stream?.has_publisher ?? false;

  $: host = $settings.hostUrl || '<host>';

  $: publishCmd = `ffmpeg -re -i input.ts -c copy -f mpegts "srt://${host}:9710?streamid=#!::m=publish,r=${name}${stream?.key_length && stream.key_length > 0 ? '&passphrase=<pass>' : ''}"`;
  $: subscribeCmd = `ffplay "srt://${host}:9710?streamid=#!::m=request,r=${name}${stream?.key_length && stream.key_length > 0 ? '&passphrase=<pass>' : ''}"`;

  function refreshThumbnail() {
    if (!hasPublisher) return;
    const token = localStorage.getItem('token') ?? '';
    thumbnailSrc = `/api/v1/streams/${encodeURIComponent(name)}/thumbnail?token=${encodeURIComponent(token)}&t=${Date.now()}`;
    thumbnailError = false;
  }

  $: if (hasPublisher) {
    refreshThumbnail();
  } else {
    thumbnailSrc = '';
  }

  onMount(async () => {
    try {
      stream = await api.getStream(name);
    } catch (e: unknown) {
      loadError = e instanceof Error ? e.message : 'Not found';
    }
    thumbnailInterval = setInterval(() => {
      if (hasPublisher) refreshThumbnail();
    }, 15000);
  });

  onDestroy(() => {
    clearInterval(thumbnailInterval);
  });

  function saveHostUrl() {
    settings.setHostUrl(hostInput);
  }

  async function copyText(text: string, which: 'publish' | 'subscribe') {
    await navigator.clipboard.writeText(text);
    if (which === 'publish') {
      copiedPublish = true;
      setTimeout(() => (copiedPublish = false), 2000);
    } else {
      copiedSubscribe = true;
      setTimeout(() => (copiedSubscribe = false), 2000);
    }
  }

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

    <!-- Preview thumbnail -->
    {#if thumbnailSrc && !thumbnailError}
      <div class="card overflow-hidden p-0">
        <img
          src={thumbnailSrc}
          alt="Stream preview"
          class="w-full object-cover rounded-lg"
          style="max-height: 360px;"
          on:error={() => { thumbnailError = true; }}
        />
      </div>
    {/if}

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
            <div class="text-2xl font-bold {metrics.rtt_ms > 500 ? 'text-red-400' : metrics.rtt_ms > 150 ? 'text-yellow-400' : 'text-sky-400'}">
              {metrics.rtt_ms.toFixed(1)} ms
            </div>
            <div class="text-xs text-gray-500 mt-1">RTT</div>
          </div>
          <div class="card text-center">
            <div class="text-2xl font-bold {metrics.send_loss_rate > 5 ? 'text-red-400' : metrics.send_loss_rate > 1 ? 'text-yellow-400' : 'text-sky-400'}">
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
          <div class="flex items-center justify-between mb-3">
            <h3 class="text-sm font-medium text-gray-400">Quick Start</h3>
            <!-- Host URL input -->
            <div class="flex items-center gap-2">
              <label for="hostUrl" class="text-xs text-gray-500 whitespace-nowrap">Host URL</label>
              <input
                id="hostUrl"
                class="input text-xs py-1 px-2 w-44"
                bind:value={hostInput}
                on:blur={saveHostUrl}
                on:keydown={(e) => e.key === 'Enter' && saveHostUrl()}
                placeholder="e.g. 212.104.141.39"
              />
            </div>
          </div>
          <div class="space-y-3">
            <div>
              <div class="text-xs text-gray-500 mb-1">Publish</div>
              <div class="flex items-start gap-2">
                <code class="flex-1 block bg-gray-950 rounded px-3 py-2 text-xs text-green-400 font-mono overflow-x-auto">
                  {publishCmd}
                </code>
                <button
                  class="btn-ghost text-xs shrink-0 px-3 py-2"
                  on:click={() => copyText(publishCmd, 'publish')}
                  title="Copy publish command"
                >
                  {#if copiedPublish}
                    <!-- checkmark -->
                    <svg xmlns="http://www.w3.org/2000/svg" class="w-4 h-4 text-green-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                    </svg>
                  {:else}
                    <!-- clipboard -->
                    <svg xmlns="http://www.w3.org/2000/svg" class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
                    </svg>
                  {/if}
                </button>
              </div>
            </div>
            <div>
              <div class="text-xs text-gray-500 mb-1">Subscribe</div>
              <div class="flex items-start gap-2">
                <code class="flex-1 block bg-gray-950 rounded px-3 py-2 text-xs text-blue-400 font-mono overflow-x-auto">
                  {subscribeCmd}
                </code>
                <button
                  class="btn-ghost text-xs shrink-0 px-3 py-2"
                  on:click={() => copyText(subscribeCmd, 'subscribe')}
                  title="Copy subscribe command"
                >
                  {#if copiedSubscribe}
                    <svg xmlns="http://www.w3.org/2000/svg" class="w-4 h-4 text-green-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                    </svg>
                  {:else}
                    <svg xmlns="http://www.w3.org/2000/svg" class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
                    </svg>
                  {/if}
                </button>
              </div>
            </div>
          </div>
        </div>
      {/if}
    </div>
  </div>
{/if}
