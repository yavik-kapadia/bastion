<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import type { StreamPayload } from '$lib/api';

  export let initial: Partial<StreamPayload> = {};
  export let submitLabel = 'Create Stream';
  export let loading = false;
  export let error = '';

  const dispatch = createEventDispatcher<{ submit: StreamPayload }>();

  let name = initial.name ?? '';
  let description = initial.description ?? '';
  let passphrase = '';
  let showPassphrase = false;
  let keyLength = initial.key_length ?? 0;

  function generatePassphrase() {
    const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*';
    const arr = new Uint8Array(24);
    crypto.getRandomValues(arr);
    passphrase = Array.from(arr, (b) => chars[b % chars.length]).join('');
    showPassphrase = true;
  }
  let maxSubscribers = initial.max_subscribers ?? 0;
  let allowedPublishers = (initial.allowed_publishers ?? []).join('\n');
  let enabled = initial.enabled ?? true;

  function handleSubmit() {
    const payload: StreamPayload = {
      name,
      description,
      key_length: keyLength,
      max_subscribers: maxSubscribers,
      allowed_publishers: allowedPublishers
        .split('\n')
        .map((s) => s.trim())
        .filter(Boolean),
      enabled
    };
    if (passphrase) payload.passphrase = passphrase;
    dispatch('submit', payload);
  }
</script>

<form on:submit|preventDefault={handleSubmit} class="space-y-5">
  {#if error}
    <div class="bg-red-900/50 border border-red-800 rounded-lg px-4 py-3 text-sm text-red-300">
      {error}
    </div>
  {/if}

  <div>
    <label class="label" for="name">Stream Name *</label>
    <input id="name" class="input" bind:value={name} required placeholder="my-stream" />
  </div>

  <div>
    <label class="label" for="description">Description</label>
    <input id="description" class="input" bind:value={description} placeholder="Optional description" />
  </div>

  <div class="border-t border-gray-800 pt-5">
    <h3 class="text-sm font-medium text-gray-400 mb-3">Encryption</h3>
    <div class="grid grid-cols-2 gap-4">
      <div>
        <label class="label" for="keyLength">Key Length</label>
        <select id="keyLength" class="input" bind:value={keyLength}>
          <option value={0}>None</option>
          <option value={16}>AES-128</option>
          <option value={24}>AES-192</option>
          <option value={32}>AES-256</option>
        </select>
      </div>
      <div>
        <label class="label" for="passphrase">
          Passphrase {keyLength > 0 ? '*' : '(optional)'}
        </label>
        <div class="flex gap-2">
          <div class="relative flex-1">
            <input
              id="passphrase"
              class="input pr-10 w-full"
              type={showPassphrase ? 'text' : 'password'}
              bind:value={passphrase}
              placeholder="min. 10 characters"
              required={keyLength > 0}
              minlength={keyLength > 0 ? 10 : undefined}
            />
            <button
              type="button"
              class="absolute inset-y-0 right-0 px-3 text-gray-400 hover:text-gray-200"
              on:click={() => (showPassphrase = !showPassphrase)}
              title={showPassphrase ? 'Hide passphrase' : 'Show passphrase'}
            >
              {showPassphrase ? '🙈' : '👁'}
            </button>
          </div>
          <button
            type="button"
            class="btn-ghost text-xs whitespace-nowrap"
            on:click={generatePassphrase}
            title="Generate random passphrase"
          >
            Generate
          </button>
        </div>
      </div>
    </div>
  </div>

  <div class="border-t border-gray-800 pt-5">
    <h3 class="text-sm font-medium text-gray-400 mb-3">Access Control</h3>
    <div class="grid grid-cols-2 gap-4">
      <div>
        <label class="label" for="maxSubs">Max Subscribers</label>
        <input
          id="maxSubs"
          class="input"
          type="number"
          bind:value={maxSubscribers}
          min="0"
          placeholder="0 = unlimited"
        />
      </div>
      <div>
        <label class="label" for="ap">Allowed Publishers (CIDR, one per line)</label>
        <textarea
          id="ap"
          class="input font-mono text-sm"
          rows="3"
          bind:value={allowedPublishers}
          placeholder="192.168.1.0/24&#10;10.0.0.1"
        ></textarea>
      </div>
    </div>
  </div>

  <div class="flex items-center gap-2">
    <input id="enabled" type="checkbox" bind:checked={enabled} class="rounded" />
    <label for="enabled" class="text-sm text-gray-300">Stream enabled</label>
  </div>

  <div class="flex justify-end">
    <button type="submit" class="btn-primary" disabled={loading}>
      {#if loading}Saving…{:else}{submitLabel}{/if}
    </button>
  </div>
</form>
