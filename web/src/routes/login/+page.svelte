<script lang="ts">
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { APIError } from '$lib/api/client';
  import { authStore, unlock } from '$lib/stores/auth';

  let password = '';
  let error = '';
  let loading = false;

  onMount(() => {
    const unsubscribe = authStore.subscribe((state) => {
      if (!state.setup) {
        goto('/setup');
      }

      if (!state.locked) {
        goto('/');
      }
    });

    return unsubscribe;
  });

  async function handleSubmit() {
    error = '';
    loading = true;

    try {
      await unlock(password);
      goto('/');
    } catch (e) {
      error = e instanceof APIError ? e.message : 'Failed to unlock Meshium';
    } finally {
      loading = false;
    }
  }
</script>

<div class="flex min-h-screen items-center justify-center bg-bg px-4 py-12">
  <div class="w-full max-w-md rounded-2xl bg-surface p-8 shadow-sm ring-1 ring-border">
    <h1 class="text-2xl font-semibold tracking-tight text-fg">Unlock Meshium</h1>
    <p class="mt-2 text-sm text-fg-muted">Enter your master password to access your servers.</p>

    {#if error}
      <div class="mt-6 rounded-lg bg-error/15 px-4 py-3 text-sm text-error ring-1 ring-error/20">
        {error}
      </div>
    {/if}

    <form class="mt-6 space-y-4" on:submit|preventDefault={handleSubmit}>
      <div>
        <label for="password" class="mb-1 block text-sm font-medium text-fg-muted">Master Password</label>
        <input
          id="password"
          type="password"
          bind:value={password}
          placeholder="Enter master password"
          class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg placeholder:text-fg-subtle shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
        />
      </div>

      <button
        type="submit"
        disabled={loading}
        class="w-full rounded-lg bg-accent px-4 py-2.5 text-sm font-medium text-accent-fg transition hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-50"
      >
        {loading ? 'Unlocking...' : 'Unlock'}
      </button>
    </form>
  </div>
</div>
