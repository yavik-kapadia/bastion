<script lang="ts">
  import { goto } from '$app/navigation';
  import { api } from '$lib/api';
  import { auth } from '$lib/stores/auth';
  import { connectWS } from '$lib/ws';

  let username = '';
  let password = '';
  let error = '';
  let loading = false;

  async function handleLogin() {
    loading = true;
    error = '';
    try {
      const res = await api.login(username, password);
      auth.login({
        token: res.token,
        userId: res.user_id,
        username: res.username,
        role: res.role
      }, res.public_host);
      connectWS(res.token);
      goto('/');
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Login failed';
    } finally {
      loading = false;
    }
  }
</script>

<div class="min-h-screen bg-gray-950 flex items-center justify-center px-4">
  <div class="w-full max-w-md">
    <div class="text-center mb-8">
      <h1 class="text-3xl font-bold text-sky-400">Bastion</h1>
      <p class="text-gray-500 mt-2">SRT Relay Dashboard</p>
    </div>

    <div class="card">
      <h2 class="text-lg font-semibold mb-6">Sign in</h2>

      {#if error}
        <div class="bg-red-900/50 border border-red-800 rounded-lg px-4 py-3 text-sm text-red-300 mb-4">
          {error}
        </div>
      {/if}

      <form on:submit|preventDefault={handleLogin} class="space-y-4">
        <div>
          <label class="label" for="username">Username</label>
          <input id="username" class="input" bind:value={username} required autocomplete="username" />
        </div>
        <div>
          <label class="label" for="password">Password</label>
          <input id="password" class="input" type="password" bind:value={password} required autocomplete="current-password" />
        </div>
        <button type="submit" class="btn-primary w-full" disabled={loading}>
          {#if loading}Signing in…{:else}Sign in{/if}
        </button>
      </form>
    </div>
  </div>
</div>
