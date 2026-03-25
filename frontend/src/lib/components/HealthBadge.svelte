<script lang="ts">
  export let health: 'green' | 'yellow' | 'red' = 'red';
  export let size: 'sm' | 'md' = 'md';

  const colors = {
    green: 'bg-green-500',
    yellow: 'bg-yellow-400',
    red: 'bg-red-500'
  };

  const labels = {
    green: 'Healthy',
    yellow: 'Degraded',
    red: 'Offline'
  };

  const sizes = {
    sm: 'w-2 h-2',
    md: 'w-3 h-3'
  };
</script>

<span class="inline-flex items-center gap-1.5">
  <span class="rounded-full {colors[health]} {sizes[size]} {health === 'green' ? 'animate-pulse' : ''}"></span>
  <span class="text-xs font-medium {health === 'green' ? 'text-green-400' : health === 'yellow' ? 'text-yellow-400' : 'text-red-400'}">
    {labels[health]}
  </span>
</span>
