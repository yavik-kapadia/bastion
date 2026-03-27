<script lang="ts">
  import '../app.css';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { getAuth, clearAuth, isAdmin } from '$lib/stores/auth.svelte';
  import { connectWS, disconnectWS } from '$lib/ws';
  import { api } from '$lib/api';

  let { data, children } = $props();

  const publicRoutes = ['/login', '/setup'];

  onMount(() => {
    if (data.auth) {
      connectWS();
    }
  });

  async function logout() {
    disconnectWS();
    try { await api.logout(); } catch { /* best effort */ }
    clearAuth();
    goto('/login');
  }

  let currentPath = $derived($page.url.pathname);
  let isPublic = $derived(publicRoutes.includes(currentPath));
</script>

{#if isPublic || !data.auth}
  {@render children()}
{:else}
  <div class="min-h-screen flex flex-col">
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
          {#if isAdmin()}
            <a href="/users" class="text-sm {currentPath.startsWith('/users') ? 'text-sky-400' : 'text-gray-400 hover:text-gray-100'} transition-colors">
              Users
            </a>
          {/if}
        </div>
        <div class="flex items-center gap-4">
          <span class="text-xs text-gray-500">{data.auth?.username} · {data.auth?.role}</span>
          <button onclick={logout} class="text-xs text-gray-500 hover:text-gray-200 transition-colors cursor-pointer py-1">Sign out</button>
        </div>
      </div>
    </nav>

    <main class="flex-1 max-w-7xl mx-auto w-full px-6 py-8">
      {@render children()}
    </main>
  </div>
{/if}
