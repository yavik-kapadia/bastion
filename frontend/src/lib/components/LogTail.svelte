<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { api, AuthError, type LogEntry } from '$lib/api';
  import { logsStore } from '$lib/ws';

  // stream = null means global feed; otherwise filter to that stream.
  let { stream = null as string | null } = $props<{ stream?: string | null }>();

  let entries = $state<LogEntry[]>([]);
  let loading = $state(true);
  let error = $state('');
  let autoscroll = $state(true);
  let scrollContainer: HTMLDivElement | undefined = $state();

  const MAX_BUFFER = 1000;

  // Initial backfill via REST.
  onMount(async () => {
    try {
      const initial = stream === null
        ? await api.globalLogs({ limit: 200 })
        : await api.streamLogs(stream, { limit: 200 });
      entries = initial ?? [];
      // After paint, scroll to bottom.
      requestAnimationFrame(scrollToBottom);
    } catch (e) {
      if (!(e instanceof AuthError)) {
        error = e instanceof Error ? e.message : 'Failed to load logs';
      }
    } finally {
      loading = false;
    }
  });

  // Subscribe to WS batches.
  const unsub = logsStore.subscribe((batch) => {
    if (!batch || !batch.records?.length) return;
    let added: LogEntry[] = [];
    for (const r of batch.records) {
      if (stream !== null && r.stream !== stream) continue;
      added.push({
        id: r.id,
        ts: r.ts,
        level: r.level,
        stream: r.stream,
        msg: r.msg,
        attrs: r.attrs ?? {}
      });
    }
    if (added.length === 0) return;
    const next = entries.concat(added);
    if (next.length > MAX_BUFFER) {
      entries = next.slice(next.length - MAX_BUFFER);
    } else {
      entries = next;
    }
    if (autoscroll) {
      requestAnimationFrame(scrollToBottom);
    }
  });

  onDestroy(unsub);

  function scrollToBottom() {
    if (!scrollContainer) return;
    scrollContainer.scrollTop = scrollContainer.scrollHeight;
  }

  // If user scrolls up, pause autoscroll. When they scroll back to bottom, resume.
  function onScroll() {
    if (!scrollContainer) return;
    const nearBottom =
      scrollContainer.scrollHeight - scrollContainer.scrollTop - scrollContainer.clientHeight < 24;
    autoscroll = nearBottom;
  }

  function jumpToBottom() {
    autoscroll = true;
    scrollToBottom();
  }

  function formatTime(tsNanos: number): string {
    const ms = tsNanos / 1_000_000;
    const d = new Date(ms);
    const h = String(d.getHours()).padStart(2, '0');
    const m = String(d.getMinutes()).padStart(2, '0');
    const s = String(d.getSeconds()).padStart(2, '0');
    const subSec = String(d.getMilliseconds()).padStart(3, '0');
    return `${h}:${m}:${s}.${subSec}`;
  }

  function levelColor(level: string): string {
    switch (level) {
      case 'error':
        return 'text-red-400';
      case 'warn':
        return 'text-yellow-400';
      case 'info':
        return 'text-sky-300';
      default:
        return 'text-gray-400';
    }
  }

  function attrsPreview(attrs: Record<string, unknown>): string {
    const keys = Object.keys(attrs ?? {});
    if (keys.length === 0) return '';
    const parts: string[] = [];
    for (const k of keys) {
      const v = attrs[k];
      let printed: string;
      if (v === null || v === undefined) {
        printed = String(v);
      } else if (typeof v === 'object') {
        printed = JSON.stringify(v);
      } else {
        printed = String(v);
      }
      parts.push(`${k}=${printed}`);
    }
    return parts.join(' ');
  }
</script>

<div class="rounded-lg border border-gray-800 overflow-hidden bg-gray-950 relative">
  <div
    bind:this={scrollContainer}
    onscroll={onScroll}
    class="h-96 overflow-y-auto font-mono text-xs leading-relaxed px-4 py-3"
  >
    {#if loading}
      <div class="text-gray-500">Loading logs...</div>
    {:else if error}
      <div class="text-red-400">{error}</div>
    {:else if entries.length === 0}
      <div class="text-gray-600">No log entries yet.</div>
    {:else}
      {#each entries as e (e.id)}
        <div class="whitespace-pre-wrap break-words py-0.5">
          <span class="text-gray-600">[{formatTime(e.ts)}]</span>
          <span class="{levelColor(e.level)} uppercase font-semibold mx-1.5">
            {e.level.padEnd(5)}
          </span>
          {#if stream === null && e.stream}
            <span class="text-purple-400 mr-1.5">{e.stream}</span>
          {/if}
          <span class="text-gray-100">{e.msg}</span>
          {#if attrsPreview(e.attrs)}
            <span class="text-gray-500 ml-1">{attrsPreview(e.attrs)}</span>
          {/if}
        </div>
      {/each}
    {/if}
  </div>

  {#if !autoscroll && !loading && !error}
    <button
      onclick={jumpToBottom}
      class="absolute bottom-3 right-3 text-xs px-2.5 py-1 rounded-full bg-sky-500 text-white shadow hover:bg-sky-400 transition-colors"
      title="Resume autoscroll"
    >
      Jump to latest
    </button>
  {/if}
</div>
