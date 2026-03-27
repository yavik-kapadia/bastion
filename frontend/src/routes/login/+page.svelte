<script lang="ts">
  import { goto, invalidateAll } from '$app/navigation';
  import { api } from '$lib/api';
  import { setAuth } from '$lib/stores/auth.svelte';
  import { connectWS } from '$lib/ws';

  let username = $state('');
  let password = $state('');
  let error = $state('');
  let loading = $state(false);

  async function handleLogin() {
    loading = true;
    error = '';
    try {
      const res = await api.login(username, password);
      setAuth(res);
      connectWS();
      await invalidateAll();
      goto('/');
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Login failed';
    } finally {
      loading = false;
    }
  }
</script>

<svelte:head><title>Login — Bastion</title></svelte:head>

<div class="min-h-screen bg-gray-950 flex items-center justify-center px-4">
  <div class="w-full max-w-md">
    <div class="text-center mb-8">
      <h1 class="text-3xl font-bold text-sky-400">Bastion</h1>
      <p class="text-gray-500 mt-2">SRT Relay Dashboard</p>
    </div>

    <div class="card">
      {#if error}
        <p class="text-sm text-red-400 bg-red-500/10 border border-red-500/20 rounded-lg px-3 py-2 mb-4">{error}</p>
      {/if}

      <form onsubmit={(e) => { e.preventDefault(); handleLogin(); }} class="space-y-4">
        <div>
          <label class="block text-xs font-medium text-gray-400 mb-1" for="username">Username</label>
          <input
            id="username"
            type="text"
            bind:value={username}
            class="input w-full"
            placeholder="admin"
            autocomplete="username"
            required
          />
        </div>
        <div>
          <label class="block text-xs font-medium text-gray-400 mb-1" for="password">Password</label>
          <input
            id="password"
            type="password"
            bind:value={password}
            class="input w-full"
            placeholder="Password"
            autocomplete="current-password"
            required
          />
        </div>
        <button type="submit" class="btn-primary w-full" disabled={loading}>
          {loading ? 'Signing in...' : 'Sign in'}
        </button>
      </form>
    </div>
  </div>
</div>
