<script lang="ts">
  import HealthBadge from './HealthBadge.svelte';
  import type { Stream } from '$lib/api';
  import type { StreamMetrics } from '$lib/ws';

  export let stream: Stream;
  export let metrics: StreamMetrics | undefined = undefined;

  $: health = metrics?.health ?? (stream.has_publisher ? 'yellow' : 'red');
  $: subscriberCount = metrics?.subscriber_count ?? stream.subscriber_count;
  $: bytesRelayed = metrics?.bytes_relayed ?? 0;

  function formatBytes(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
    return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`;
  }
</script>

<a href="/streams/{stream.name}" class="card hover:border-gray-700 transition-colors block group">
  <div class="flex items-start justify-between mb-3">
    <div>
      <h3 class="font-semibold text-gray-100 group-hover:text-sky-400 transition-colors">
        {stream.name}
      </h3>
      {#if stream.description}
        <p class="text-xs text-gray-500 mt-0.5 truncate max-w-[200px]">{stream.description}</p>
      {/if}
    </div>
    <HealthBadge {health} />
  </div>

  <div class="grid grid-cols-3 gap-3 mt-4">
    <div class="text-center">
      <div class="text-lg font-bold text-gray-100">{subscriberCount}</div>
      <div class="text-xs text-gray-500">Viewers</div>
    </div>
    <div class="text-center">
      <div class="text-lg font-bold text-gray-100">
        {stream.key_length > 0 ? `AES-${stream.key_length * 8}` : '—'}
      </div>
      <div class="text-xs text-gray-500">Encryption</div>
    </div>
    <div class="text-center">
      <div class="text-lg font-bold text-gray-100">{formatBytes(bytesRelayed)}</div>
      <div class="text-xs text-gray-500">Relayed</div>
    </div>
  </div>

  {#if !stream.enabled}
    <div class="mt-3 text-xs text-center text-gray-600 uppercase tracking-wider">Disabled</div>
  {/if}
</a>
