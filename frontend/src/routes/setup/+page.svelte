<script lang="ts">
  import { goto, invalidateAll } from '$app/navigation';
  import { setAuth } from '$lib/stores/auth.svelte';
  import { api } from '$lib/api';
  import { connectWS } from '$lib/ws';

  let username = $state('');
  let password = $state('');
  let confirmPw = $state('');
  let error = $state('');
  let loading = $state(false);

  async function handleSubmit() {
    error = '';
    if (!username || !password) { error = 'Username and password are required.'; return; }
    if (password !== confirmPw) { error = 'Passwords do not match.'; return; }
    if (password.length < 8) { error = 'Password must be at least 8 characters.'; return; }

    loading = true;
    try {
      const res = await api.setup(username, password);
      setAuth(res);
      connectWS();
      await invalidateAll();
      goto('/');
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Setup failed.';
    } finally {
      loading = false;
    }
  }
</script>

<svelte:head><title>Setup — Bastion</title></svelte:head>

<div class="min-h-screen bg-gray-950 flex items-center justify-center px-4">
  <div class="w-full max-w-sm">
    <div class="text-center mb-8">
      <div class="inline-flex items-center justify-center w-14 h-14 rounded-2xl bg-sky-500/10 border border-sky-500/20 mb-4">
        <svg class="w-7 h-7 text-sky-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
            d="M8 9l3 3-3 3m5 0h3M5 20h14a2 2 0 002-2V6a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z" />
        </svg>
      </div>
      <h1 class="text-2xl font-bold text-white">Welcome to Bastion</h1>
      <p class="text-sm text-gray-400 mt-1">Create your admin account to get started.</p>
    </div>

    <form onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="card space-y-4">
      {#if error}
        <p class="text-sm text-red-400 bg-red-500/10 border border-red-500/20 rounded-lg px-3 py-2">{error}</p>
      {/if}

      <div>
        <label class="block text-xs font-medium text-gray-400 mb-1" for="username">Username</label>
        <input id="username" type="text" bind:value={username} class="input w-full" placeholder="admin" autocomplete="username" required />
      </div>
      <div>
        <label class="block text-xs font-medium text-gray-400 mb-1" for="password">Password</label>
        <input id="password" type="password" bind:value={password} class="input w-full" placeholder="At least 8 characters" autocomplete="new-password" required />
      </div>
      <div>
        <label class="block text-xs font-medium text-gray-400 mb-1" for="confirm">Confirm password</label>
        <input id="confirm" type="password" bind:value={confirmPw} class="input w-full" placeholder="Repeat password" autocomplete="new-password" required />
      </div>
      <button type="submit" class="btn-primary w-full" disabled={loading}>
        {loading ? 'Creating account...' : 'Create admin account'}
      </button>
    </form>
  </div>
</div>
