<script lang="ts">
  import { page } from '$app/stores';
  import { goto, invalidateAll } from '$app/navigation';
  import { onDestroy } from 'svelte';
  import { api } from '$lib/api';
  import { metricsStore } from '$lib/ws';
  import { isManager } from '$lib/stores/auth.svelte';
  import { getHostUrl, setHostUrl, resolvedHost } from '$lib/stores/settings.svelte';
  import { getAuth } from '$lib/stores/auth.svelte';
  import HealthBadge from '$lib/components/HealthBadge.svelte';
  import StreamForm from '$lib/components/StreamForm.svelte';
  import LogTail from '$lib/components/LogTail.svelte';
  import type { Stream, StreamPayload } from '$lib/api';

  let { data } = $props();
  let stream = $derived(data.stream);
  let editing = $state(false);
  let updateLoading = $state(false);
  let updateError = $state('');
  let deleteLoading = $state(false);
  // Pre-fetched on entering edit mode so the StreamForm can prefill the
  // passphrase field. Null until fetched / not yet revealed.
  let editPassphrase = $state<string | null>(null);
  let editLoading = $state(false);

  async function startEditing() {
    updateError = '';
    if (stream?.key_length && stream.key_length > 0) {
      editLoading = true;
      try {
        const revealed = await api.getStream(name, true);
        editPassphrase = revealed.passphrase ?? '';
      } catch (e) {
        // Manager+ should always be allowed to reveal; if it fails, leave
        // the field blank so the user can re-enter or skip the change.
        editPassphrase = '';
      } finally {
        editLoading = false;
      }
    } else {
      editPassphrase = '';
    }
    editing = true;
  }

  function cancelEditing() {
    editing = false;
    editPassphrase = null;
    updateError = '';
  }

  let hostInput = $state(getHostUrl());

  let thumbnailSrc = $state('');
  let thumbnailError = $state(false);

  let copiedPublish = $state(false);
  let copiedSubscribe = $state(false);

  let name = $derived($page.params.name ?? '');
  let metrics = $derived($metricsStore?.streams[name]);
  let health = $derived(metrics?.health ?? (stream?.has_publisher ? 'yellow' : 'red'));
  let hasPublisher = $derived(metrics?.has_publisher ?? stream?.has_publisher ?? false);

  let host = $derived(resolvedHost() || '<host>');

  // External SRT port surfaced to users in the Quick Start commands.
  // Defaults to 9710 (the standard SRT relay port); admins can override via
  // `[srt].external_port` in bastion.toml when they NAT a different host
  // port (e.g. 443/udp to look like QUIC for restrictive firewalls).
  let externalPort = $derived(getAuth()?.external_port || 9710);

  let hasPass = $derived(stream?.key_length && stream.key_length > 0);
  let displaySuffix = $derived(hasPass ? '&passphrase=••••••••' : '');

  // SRT latency suffix appended to URL when the stream has a per-stream
  // override set. The relay's listener latency is the floor; the caller
  // can ask for more via &latency=<microseconds>. Bastion negotiates the
  // higher of the two, which is what we want for WAN viewers.
  let latencySuffix = $derived(
    stream?.latency_ms && stream.latency_ms > 0 ? `&latency=${stream.latency_ms * 1000}` : ''
  );

  // Use SINGLE quotes around the SRT URL: in zsh (and many bash setups),
  // double quotes don't suppress history expansion of `!`, which mangles
  // the streamid `#!::` prefix when pasted into a terminal. Single quotes
  // preserve `!`, `#`, `&`, and `:` literally.
  let publishDisplay = $derived(
    `ffmpeg -re -i input.ts -c copy -f mpegts 'srt://${host}:${externalPort}?streamid=#!::m=publish,r=${name}${displaySuffix}${latencySuffix}'`
  );
  let subscribeDisplay = $derived(
    `ffplay 'srt://${host}:${externalPort}?streamid=#!::m=request,r=${name}${displaySuffix}${latencySuffix}'`
  );

  // Fetch passphrase on demand and build the real command for clipboard
  async function buildCopyCmd(mode: 'publish' | 'subscribe'): Promise<string> {
    let passSuffix = '';
    if (hasPass) {
      try {
        const revealed = await api.getStream(name, true);
        passSuffix = `&passphrase=${revealed.passphrase || '<pass>'}`;
      } catch {
        passSuffix = '&passphrase=<pass>';
      }
    }
    const lat = stream?.latency_ms && stream.latency_ms > 0 ? `&latency=${stream.latency_ms * 1000}` : '';
    if (mode === 'publish') {
      return `ffmpeg -re -i input.ts -c copy -f mpegts 'srt://${host}:${externalPort}?streamid=#!::m=publish,r=${name}${passSuffix}${lat}'`;
    }
    return `ffplay 'srt://${host}:${externalPort}?streamid=#!::m=request,r=${name}${passSuffix}${lat}'`;
  }

  function refreshThumbnail() {
    if (!hasPublisher) return;
    // Cookie is sent automatically, no token param needed
    thumbnailSrc = `/api/v1/streams/${encodeURIComponent(name)}/thumbnail?t=${Date.now()}`;
    thumbnailError = false;
  }

  $effect(() => {
    if (hasPublisher) {
      refreshThumbnail();
    } else {
      thumbnailSrc = '';
    }
  });

  // Refresh cadence is configured server-side (cfg.Dashboard.ThumbnailRefreshRate)
  // and surfaced via /auth/me. Falls back to 15s if older server / not set.
  const refreshMs = getAuth()?.thumbnail_refresh_ms ?? 15000;
  const thumbnailInterval = setInterval(() => {
    if (hasPublisher) refreshThumbnail();
  }, refreshMs);

  onDestroy(() => {
    clearInterval(thumbnailInterval);
  });

  function saveHostUrl() {
    setHostUrl(hostInput);
  }

  async function copyCmd(which: 'publish' | 'subscribe') {
    const text = await buildCopyCmd(which);
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      document.execCommand('copy');
      document.body.removeChild(ta);
    }
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
      await api.updateStream(name, e.detail);
      editing = false;
      await invalidateAll();
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

  // ffprobe reports frame rates as "num/den" — render either an integer
  // ("120") or a decimal with 2 places for fractional rates ("29.97 fps").
  function formatFps(raw: string): string {
    if (!raw) return '—';
    const [n, d] = raw.split('/').map(Number);
    if (!d) return raw;
    const fps = n / d;
    if (Number.isInteger(fps)) return `${fps} fps`;
    return `${fps.toFixed(2)} fps`;
  }
</script>

<svelte:head><title>{name} — Bastion</title></svelte:head>

<div class="space-y-6 max-w-4xl">
  <!-- Header -->
  <div class="flex items-start justify-between">
    <div>
      <a href="/streams" class="text-sm text-gray-500 hover:text-gray-300">&larr; Streams</a>
      <div class="flex items-center gap-3 mt-2">
        <h1 class="text-2xl font-bold">{stream.name}</h1>
        <HealthBadge {health} />
      </div>
      {#if stream.description}
        <p class="text-gray-500 text-sm mt-1">{stream.description}</p>
      {/if}
    </div>
    {#if isManager()}
      <div class="flex gap-2">
        <button
          class="btn-ghost"
          disabled={editLoading}
          onclick={() => (editing ? cancelEditing() : startEditing())}
        >
          {editing ? 'Cancel' : editLoading ? 'Loading…' : 'Edit'}
        </button>
        <button class="btn-danger" onclick={handleDelete} disabled={deleteLoading}>
          {deleteLoading ? 'Deleting...' : 'Delete'}
        </button>
      </div>
    {/if}
  </div>

  <!-- Preview thumbnail. object-contain (not object-cover) so unusual aspect
       ratios show the full frame letter-/pillarboxed rather than cropped. -->
  {#if thumbnailSrc && !thumbnailError}
    <div class="card overflow-hidden p-0 bg-black flex items-center justify-center">
      <img
        src={thumbnailSrc}
        alt="Stream preview"
        class="w-full h-auto max-h-[480px] object-contain rounded-lg"
        onerror={() => { thumbnailError = true; }}
      />
    </div>
  {/if}

  <!-- What the publisher is actually sending. Populated by an ffprobe of the
       live stream ~5s after the publisher attaches; null until then. -->
  {#if stream?.media_info}
    {@const mi = stream.media_info}
    <div class="card">
      <div class="text-sm font-semibold text-gray-200 mb-3">Publisher Media</div>
      <div class="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 gap-x-6 gap-y-2 text-sm">
        <div>
          <div class="text-xs text-gray-500">Resolution</div>
          <div class="text-gray-100">{mi.width} × {mi.height}</div>
        </div>
        <div>
          <div class="text-xs text-gray-500">Frame Rate</div>
          <div class="text-gray-100">{formatFps(mi.fps)}</div>
        </div>
        <div>
          <div class="text-xs text-gray-500">Codec</div>
          <div class="text-gray-100">{mi.codec}{mi.profile ? ` (${mi.profile})` : ''}</div>
        </div>
        {#if mi.bit_rate_kbps}
          <div>
            <div class="text-xs text-gray-500">Bitrate</div>
            <div class="text-gray-100">{(mi.bit_rate_kbps / 1000).toFixed(2)} Mbps</div>
          </div>
        {/if}
        {#if mi.pix_fmt}
          <div>
            <div class="text-xs text-gray-500">Pixel Format</div>
            <div class="text-gray-100">{mi.pix_fmt}</div>
          </div>
        {/if}
        {#if mi.color_space}
          <div>
            <div class="text-xs text-gray-500">Color Space</div>
            <div class="text-gray-100">{mi.color_space}{mi.color_range ? ` (${mi.color_range})` : ''}</div>
          </div>
        {/if}
      </div>
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
    {#if editing && isManager()}
      <StreamForm
        initial={{ ...stream, passphrase: editPassphrase ?? '' }}
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
          <div class="flex items-center gap-2">
            <label for="hostUrl" class="text-xs text-gray-500 whitespace-nowrap">Host URL</label>
            <input
              id="hostUrl"
              class="input text-xs py-1 px-2 w-44"
              bind:value={hostInput}
              onblur={saveHostUrl}
              onkeydown={(e) => { if (e.key === 'Enter') saveHostUrl(); }}
              placeholder="e.g. 212.104.141.39"
            />
          </div>
        </div>
        <div class="space-y-3">
          <div>
            <div class="text-xs text-gray-500 mb-1">Publish</div>
            <div class="flex items-start gap-2">
              <code class="flex-1 block bg-gray-950 rounded px-3 py-2 text-xs text-green-400 font-mono overflow-x-auto">
                {publishDisplay}
              </code>
              <button
                class="btn-ghost text-xs shrink-0 px-3 py-2"
                onclick={() => copyCmd('publish')}
                title="Copy publish command"
              >
                {#if copiedPublish}
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
          <div>
            <div class="text-xs text-gray-500 mb-1">Subscribe</div>
            <div class="flex items-start gap-2">
              <code class="flex-1 block bg-gray-950 rounded px-3 py-2 text-xs text-blue-400 font-mono overflow-x-auto">
                {subscribeDisplay}
              </code>
              <button
                class="btn-ghost text-xs shrink-0 px-3 py-2"
                onclick={() => copyCmd('subscribe')}
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

  <!-- Logs -->
  {#if isManager()}
    <div class="card">
      <div class="flex items-center justify-between mb-3">
        <h2 class="font-semibold">Logs</h2>
        <span class="text-xs text-gray-500">live · last 24h</span>
      </div>
      <LogTail stream={name} />
    </div>
  {/if}
</div>
