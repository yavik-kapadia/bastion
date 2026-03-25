<script lang="ts">
  import '../app.css';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { auth, isLoggedIn } from '$lib/stores/auth';
  import { connectWS, disconnectWS } from '$lib/ws';
  import { onMount } from 'svelte';

  onMount(() => {
    if ($isLoggedIn) {
      connectWS($auth!.token);
    }
  });

  function logout() {
    disconnectWS();
    auth.logout();
    goto('/login');
  }

  $: currentPath = $page.url.pathname;
</script>

{#if !$isLoggedIn && currentPath !== '/login'}
  <script>
    window.location.href = '/login';
  </script>
{:else if $isLoggedIn}
  <div class="min-h-screen flex flex-col">
    <!-- Top nav -->
    <nav class="bg-gray-900 border-b border-gray-800 px-6 py-3">
      <div class="max-w-7xl mx-auto flex items-center justify-between">
        <div class="flex items-center gap-6">
          <a href="/" class="flex items-center gap-2 font-bold text-sky-400 text-lg">
            <svg class="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
                d="M8 9l3 3-3 3m5 0h3M5 20h14a2 2 0 002-2V6a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z" />
            </svg>
            Bastion
          </a>
          <a href="/" class="text-sm {currentPath === '/' ? 'text-sky-400' : 'text-gray-400 hover:text-gray-100'} transition-colors">
            Dashboard
          </a>
          <a href="/streams" class="text-sm {currentPath.startsWith('/streams') ? 'text-sky-400' : 'text-gray-400 hover:text-gray-100'} transition-colors">
            Streams
          </a>
        </div>
        <div class="flex items-center gap-4">
          <span class="text-xs text-gray-500">{$auth?.username} · {$auth?.role}</span>
          <button on:click={logout} class="text-xs btn-ghost py-1">Sign out</button>
        </div>
      </div>
    </nav>

    <!-- Content -->
    <main class="flex-1 max-w-7xl mx-auto w-full px-6 py-8">
      <slot />
    </main>
  </div>
{:else}
  <slot />
{/if}
