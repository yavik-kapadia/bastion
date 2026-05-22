<script lang="ts">
  import LogTail from '$lib/components/LogTail.svelte';
</script>

<svelte:head><title>Logs — Bastion</title></svelte:head>

<div class="space-y-6 max-w-5xl">
  <div>
    <h1 class="text-2xl font-bold">Logs</h1>
    <p class="text-gray-500 text-sm mt-1">
      Live event feed across all streams and the server. Retains 24 hours of history.
    </p>
  </div>

  <LogTail stream={null} />
</div>
