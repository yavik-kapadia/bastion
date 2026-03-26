<script lang="ts">
  import '../app.css';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { auth, isLoggedIn, isAdmin } from '$lib/stores/auth';
  import { connectWS, disconnectWS } from '$lib/ws';
  import { api } from '$lib/api';

  const publicRoutes = ['/login', '/setup'];

  onMount(async () => {
    const path = $page.url.pathname;

    // Always check setup status first.
    try {
      const { needs_setup } = await api.setupStatus();
      if (needs_setup && path !== '/setup') {
        goto('/setup');
        return;
      }
      if (!needs_setup && path === '/setup') {
        goto('/login');
        return;
      }
    } catch {
      // If we can't reach the API, let the current route handle it.
    }

    if ($isLoggedIn) {
      connectWS($auth!.token);
    } else if (!publicRoutes.includes(path)) {
      goto('/login');
    }
  });

  function logout() {
    disconnectWS();
    auth.logout();
    goto('/login');
  }

  $: currentPath = $page.url.pathname;
  $: isPublic = publicRoutes.includes(currentPath);
</script>

{#if isPublic || !$isLoggedIn}
  <slot />
{:else}
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
          {#if $isAdmin}
            <a href="/users" class="text-sm {currentPath.startsWith('/users') ? 'text-sky-400' : 'text-gray-400 hover:text-gray-100'} transition-colors">
              Users
            </a>
          {/if}
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
{/if}
