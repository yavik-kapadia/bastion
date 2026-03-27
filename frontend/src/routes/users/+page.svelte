<script lang="ts">
  import { invalidateAll } from '$app/navigation';
  import { getAuth } from '$lib/stores/auth.svelte';
  import { api } from '$lib/api';
  import type { User } from '$lib/api';

  let { data } = $props();
  let users: User[] = $state(data.users);

  let newUsername = $state('');
  let newPassword = $state('');
  let newRole = $state('viewer');
  let creating = $state(false);
  let createError = $state('');
  let createSuccess = $state('');

  const roles = [
    { value: 'viewer',  label: 'Viewer',  desc: 'Read-only access' },
    { value: 'manager', label: 'Manager', desc: 'Create and manage streams' },
    { value: 'admin',   label: 'Admin',   desc: 'Full access including user management' },
  ];

  const auth = getAuth();

  async function createUser() {
    createError = '';
    createSuccess = '';
    if (!newUsername || !newPassword) { createError = 'Username and password are required.'; return; }
    if (newPassword.length < 8) { createError = 'Password must be at least 8 characters.'; return; }
    creating = true;
    try {
      await api.createUser(newUsername, newPassword, newRole);
      createSuccess = `User "${newUsername}" created.`;
      newUsername = '';
      newPassword = '';
      newRole = 'viewer';
      users = await api.listUsers();
    } catch (e: unknown) {
      createError = e instanceof Error ? e.message : 'Create failed';
    } finally {
      creating = false;
    }
  }

  async function deleteUser(id: string, username: string) {
    if (id === auth?.user_id) { alert("You can't delete your own account."); return; }
    if (!confirm(`Delete user "${username}"?`)) return;
    try {
      await api.deleteUser(id);
      users = await api.listUsers();
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : 'Delete failed');
    }
  }

  const roleBadge: Record<string, string> = {
    admin:   'bg-red-500/10 text-red-400 border-red-500/20',
    manager: 'bg-amber-500/10 text-amber-400 border-amber-500/20',
    viewer:  'bg-gray-500/10 text-gray-400 border-gray-500/20',
  };
</script>

<svelte:head><title>Users — Bastion</title></svelte:head>

<div class="space-y-8">
  <div class="flex items-center justify-between">
    <div>
      <h1 class="text-2xl font-bold text-white">Users</h1>
      <p class="text-sm text-gray-400 mt-0.5">Manage who has access to Bastion.</p>
    </div>
  </div>

  <div class="card">
    <h2 class="text-sm font-semibold text-white mb-4">Add user</h2>

    {#if createError}
      <p class="text-sm text-red-400 bg-red-500/10 border border-red-500/20 rounded-lg px-3 py-2 mb-4">{createError}</p>
    {/if}
    {#if createSuccess}
      <p class="text-sm text-green-400 bg-green-500/10 border border-green-500/20 rounded-lg px-3 py-2 mb-4">{createSuccess}</p>
    {/if}

    <form onsubmit={(e) => { e.preventDefault(); createUser(); }} class="grid grid-cols-1 sm:grid-cols-4 gap-3 items-end">
      <div>
        <label class="block text-xs font-medium text-gray-400 mb-1" for="newUsername">Username</label>
        <input id="newUsername" type="text" bind:value={newUsername} class="input w-full" placeholder="jane" required />
      </div>
      <div>
        <label class="block text-xs font-medium text-gray-400 mb-1" for="newPassword">Password</label>
        <input id="newPassword" type="password" bind:value={newPassword} class="input w-full" placeholder="Min 8 chars" required />
      </div>
      <div>
        <label class="block text-xs font-medium text-gray-400 mb-1" for="newRole">Role</label>
        <select id="newRole" bind:value={newRole} class="input w-full">
          {#each roles as r}
            <option value={r.value}>{r.label} — {r.desc}</option>
          {/each}
        </select>
      </div>
      <button type="submit" class="btn-primary" disabled={creating}>
        {creating ? 'Creating...' : 'Create user'}
      </button>
    </form>
  </div>

  <div class="card p-0 overflow-hidden">
    {#if users.length === 0}
      <div class="p-8 text-center text-gray-500 text-sm">No users yet.</div>
    {:else}
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-gray-800">
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-400">Username</th>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-400">Role</th>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-400">Created</th>
            <th class="px-4 py-3"></th>
          </tr>
        </thead>
        <tbody>
          {#each users as user}
            <tr class="border-b border-gray-800/50 hover:bg-gray-800/30 transition-colors">
              <td class="px-4 py-3 text-gray-200 font-medium">
                {user.username}
                {#if user.id === auth?.user_id}
                  <span class="ml-2 text-xs text-gray-500">(you)</span>
                {/if}
              </td>
              <td class="px-4 py-3">
                <span class="inline-flex items-center px-2 py-0.5 rounded-full text-xs border {roleBadge[user.role] ?? roleBadge.viewer}">
                  {user.role}
                </span>
              </td>
              <td class="px-4 py-3 text-gray-500 text-xs">
                {user.created_at ? new Date(user.created_at).toLocaleDateString() : ''}
              </td>
              <td class="px-4 py-3 text-right">
                {#if user.id !== auth?.user_id}
                  <button
                    onclick={() => deleteUser(user.id, user.username)}
                    class="text-xs text-red-400 hover:text-red-300 transition-colors"
                  >
                    Delete
                  </button>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </div>
</div>
