<script lang="ts">
  import { onMount } from 'svelte';
  import { ArrowLeft, Check, Copy, Lock } from 'lucide-svelte';
  import { api } from '$lib/api/client';
  import { lock } from '$lib/stores/auth';

  let publicKey = '';
  let loading = true;
  let copied = false;
  let regenerating = false;
  let error = '';

  onMount(async () => {
    try {
      const res = (await api.get('/ssh-key/public')) as { publicKey: string };
      publicKey = res.publicKey;
    } catch {
      error = 'Failed to load SSH public key';
    } finally {
      loading = false;
    }
  });

  async function copyKey() {
    if (!publicKey) return;

    await navigator.clipboard.writeText(publicKey);
    copied = true;
    setTimeout(() => (copied = false), 2000);
  }

  async function regenerate() {
    if (!confirm('Regenerate SSH key pair? All servers using the old key will need re-authentication.')) {
      return;
    }

    regenerating = true;

    try {
      const res = (await api.post('/ssh-key/regenerate')) as { publicKey: string };
      publicKey = res.publicKey;
    } catch {
      error = 'Failed to regenerate SSH key pair';
    } finally {
      regenerating = false;
    }
  }
</script>

<div class="min-h-screen bg-slate-50">
  <header class="border-b border-slate-200 bg-white px-6 py-4">
    <a href="/" class="inline-flex items-center gap-2 text-sm text-slate-600 transition hover:text-slate-900">
      <ArrowLeft size={16} /> Back to Servers
    </a>
    <div class="mt-3 flex flex-wrap items-center justify-between gap-4">
      <div>
        <h1 class="text-xl font-bold tracking-tight text-slate-900">Settings</h1>
        <p class="mt-1 text-sm text-slate-500">Manage the app SSH key pair and session access.</p>
      </div>
      <button
        type="button"
        on:click={() => lock()}
        class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50"
      >
        <Lock size={16} /> Lock App
      </button>
    </div>
  </header>

  <main class="mx-auto w-full max-w-3xl p-6">
    {#if error}
      <div class="mb-4 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{error}</div>
    {/if}

    <section class="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
      <div class="mb-4">
        <h2 class="text-lg font-semibold text-slate-900">SSH Public Key</h2>
        <p class="mt-1 text-sm text-slate-500">
          Meshium can use this key for passwordless SSH authentication and key installation.
        </p>
      </div>

      {#if loading}
        <div class="rounded-lg border border-dashed border-slate-300 bg-slate-50 px-4 py-8 text-center text-slate-500">
          Loading SSH key...
        </div>
      {:else}
        <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
          <div class="mb-3 flex items-center justify-between gap-3">
            <span class="text-sm font-medium text-slate-700">Public Key</span>
            <button
              type="button"
              on:click={copyKey}
              class="inline-flex items-center gap-2 text-sm font-medium text-blue-600 transition hover:text-blue-700"
            >
              {#if copied}
                <Check size={14} /> Copied
              {:else}
                <Copy size={14} /> Copy
              {/if}
            </button>
          </div>
          <pre class="overflow-x-auto whitespace-pre-wrap break-all rounded-lg bg-white p-3 font-mono text-xs text-slate-700">{publicKey || 'No SSH public key has been generated yet.'}</pre>
        </div>

        <div class="mt-4 flex flex-wrap gap-3">
          <button
            type="button"
            on:click={regenerate}
            disabled={regenerating}
            class="inline-flex items-center justify-center rounded-lg border border-red-300 bg-white px-4 py-2 text-sm font-medium text-red-600 transition hover:bg-red-50 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {regenerating ? 'Regenerating...' : 'Regenerate Key Pair'}
          </button>
        </div>
      {/if}
    </section>
  </main>
</div>
