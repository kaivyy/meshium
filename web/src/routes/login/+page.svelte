<script lang="ts">
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
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
        goto('/servers');
      }
    });

    return unsubscribe;
  });

  async function handleSubmit() {
    error = '';
    loading = true;

    try {
      await unlock(password);
      goto('/servers');
    } catch {
      error = 'Invalid password';
    } finally {
      loading = false;
    }
  }
</script>

<div class="flex min-h-screen items-center justify-center bg-slate-50 px-4 py-12">
  <div class="w-full max-w-md rounded-2xl bg-white p-8 shadow-sm ring-1 ring-slate-200">
    <h1 class="text-2xl font-semibold tracking-tight text-slate-900">Unlock Meshium</h1>
    <p class="mt-2 text-sm text-slate-600">Enter your master password to access your servers.</p>

    {#if error}
      <div class="mt-6 rounded-lg bg-red-50 px-4 py-3 text-sm text-red-700 ring-1 ring-red-200">
        {error}
      </div>
    {/if}

    <form class="mt-6 space-y-4" on:submit|preventDefault={handleSubmit}>
      <div>
        <label for="password" class="mb-1 block text-sm font-medium text-slate-700">Master Password</label>
        <input
          id="password"
          type="password"
          bind:value={password}
          placeholder="Enter master password"
          class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 placeholder:text-slate-400 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
        />
      </div>

      <button
        type="submit"
        disabled={loading}
        class="w-full rounded-lg bg-blue-600 px-4 py-2.5 text-sm font-medium text-white transition hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
      >
        {loading ? 'Unlocking...' : 'Unlock'}
      </button>
    </form>
  </div>
</div>
