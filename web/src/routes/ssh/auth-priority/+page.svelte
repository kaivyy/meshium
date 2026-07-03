<script lang="ts">
  import { onMount } from 'svelte';
  import { GripVertical, Save, ToggleLeft, ToggleRight, ArrowUp, ArrowDown, RotateCcw } from 'lucide-svelte';
  import { api } from '$lib/api/client';

  interface AuthPriorityEntry {
    method: string;
    priority: number;
    enabled: boolean;
  }

  const methodDescriptions: Record<string, string> = {
    agent: 'Prefer identities loaded in the local SSH agent.',
    ed25519: 'Fast modern key algorithm with strong security.',
    rsa: 'Compatibility with older environments and tooling.',
    ecdsa: 'Hardware-backed and standards-friendly key type.',
    'keyboard-interactive': 'Prompt-based challenge flow for interactive MFA.',
    password: 'Fallback password authentication.'
  };

  const recommendedOrder = ['agent', 'ed25519', 'rsa', 'ecdsa', 'keyboard-interactive', 'password'];

  let entries: AuthPriorityEntry[] = [];
  let loading = true;
  let saving = false;
  let error = '';
  let success = '';
  let dragIndex: number | null = null;
  let snapshot = '';

  onMount(loadEntries);

  async function loadEntries() {
    loading = true;
    error = '';
    success = '';

    try {
      const data = (await api.get('/auth-priority')) as Array<AuthPriorityEntry>;
      entries = normalizeEntries(data.length ? data : recommendedOrder.map((method, index) => ({ method, priority: index + 1, enabled: true })));
      snapshot = JSON.stringify(entries);
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to load auth priority';
    } finally {
      loading = false;
    }
  }

  function normalizeEntries(items: AuthPriorityEntry[]) {
    return [...items]
      .sort((a, b) => a.priority - b.priority)
      .map((entry, index) => ({ ...entry, priority: index + 1 }));
  }

  function moveItem(from: number, to: number) {
    if (from === to || from < 0 || to < 0 || from >= entries.length || to >= entries.length) return;

    const next = [...entries];
    const [item] = next.splice(from, 1);
    next.splice(to, 0, item);
    entries = normalizeEntries(next);
  }

  function toggleEnabled(index: number) {
    entries = entries.map((entry, i) => (i === index ? { ...entry, enabled: !entry.enabled } : entry));
  }

  function moveUp(index: number) {
    moveItem(index, index - 1);
  }

  function moveDown(index: number) {
    moveItem(index, index + 1);
  }

  function resetToRecommended() {
    entries = normalizeEntries(
      recommendedOrder.map((method, index) => {
        const existing = entries.find((entry) => entry.method === method);
        return existing ?? { method, priority: index + 1, enabled: true };
      })
    );
  }

  async function saveEntries() {
    error = '';
    success = '';
    saving = true;

    try {
      const payload = entries.map((entry, index) => ({
        method: entry.method,
        priority: index + 1,
        enabled: entry.enabled
      }));
      await api.put('/auth-priority', payload);
      entries = normalizeEntries(payload);
      snapshot = JSON.stringify(entries);
      success = 'Auth priority saved successfully.';
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to save auth priority';
    } finally {
      saving = false;
    }
  }

  function onDragStart(index: number) {
    dragIndex = index;
  }

  function onDrop(index: number) {
    if (dragIndex === null) return;
    moveItem(dragIndex, index);
    dragIndex = null;
  }

  $: dirty = JSON.stringify(entries) !== snapshot;
</script>

<svelte:head>
  <title>Auth Priority</title>
</svelte:head>

<div class="p-6">
  <div class="mx-auto flex max-w-5xl flex-col gap-6">
    <div class="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
      <div class="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <div class="inline-flex items-center gap-2 rounded-full bg-blue-50 px-3 py-1 text-xs font-medium text-blue-700">
            <ToggleRight size={14} /> SSH Auth Flow
          </div>
          <h1 class="mt-3 text-2xl font-bold tracking-tight text-slate-900">Auth Priority</h1>
          <p class="mt-2 max-w-3xl text-sm text-slate-500">
            Drag to reorder authentication methods and toggle strategies on or off.
          </p>
        </div>

        <div class="flex flex-wrap gap-3">
          <button
            type="button"
            on:click={resetToRecommended}
            class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50"
          >
            <RotateCcw size={16} /> Reset
          </button>
          <button
            type="button"
            on:click={saveEntries}
            disabled={!dirty || saving}
            class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
          >
            <Save size={16} />
            {saving ? 'Saving...' : 'Save Changes'}
          </button>
        </div>
      </div>

      <div class="mt-6 grid gap-4 md:grid-cols-3">
        <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
          <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Enabled Methods</p>
          <p class="mt-2 text-2xl font-bold text-slate-900">{entries.filter((entry) => entry.enabled).length}</p>
        </div>
        <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
          <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Disabled Methods</p>
          <p class="mt-2 text-2xl font-bold text-slate-900">{entries.filter((entry) => !entry.enabled).length}</p>
        </div>
        <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
          <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Order Changes</p>
          <p class="mt-2 text-2xl font-bold text-slate-900">{dirty ? 'Unsaved' : 'Saved'}</p>
        </div>
      </div>
    </div>

    {#if error}
      <div class="rounded-2xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700" role="alert">{error}</div>
    {/if}

    {#if success}
      <div class="rounded-2xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-700">{success}</div>
    {/if}

    {#if loading}
      <div class="rounded-2xl border border-dashed border-slate-300 bg-white px-4 py-8 text-center text-slate-500">Loading auth methods...</div>
    {:else}
      <div class="rounded-2xl border border-slate-200 bg-white shadow-sm">
        <div class="border-b border-slate-200 px-5 py-4">
          <h2 class="text-sm font-semibold uppercase tracking-wide text-slate-500">Priority Order</h2>
          <p class="mt-1 text-sm text-slate-500">The top-most enabled method is attempted first.</p>
        </div>

        <div class="divide-y divide-slate-100" role="list">
          {#each entries as entry, index}
            <div
              class={`flex flex-col gap-4 p-5 transition ${dragIndex === index ? 'bg-blue-50/50' : ''}`}
              role="listitem"
              draggable="true"
              on:dragstart={() => onDragStart(index)}
              on:dragover|preventDefault
              on:drop={() => onDrop(index)}
            >
              <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
                <div class="flex items-start gap-4">
                  <div class="flex items-center gap-2 rounded-xl border border-slate-200 bg-slate-50 px-3 py-2 text-slate-500">
                    <GripVertical size={18} />
                    <span class="text-xs font-medium uppercase tracking-wide">{entry.priority}</span>
                  </div>
                  <div>
                    <div class="flex flex-wrap items-center gap-2">
                      <p class="font-medium text-slate-900">{entry.method}</p>
                      <span class="rounded-full bg-slate-100 px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide text-slate-600">
                        {entry.enabled ? 'Enabled' : 'Disabled'}
                      </span>
                    </div>
                    <p class="mt-1 text-sm text-slate-500">{methodDescriptions[entry.method] || 'Custom or unsupported authentication method.'}</p>
                  </div>
                </div>

                <div class="flex flex-wrap items-center gap-2">
                  <button
                    type="button"
                    on:click={() => toggleEnabled(index)}
                    class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50"
                  >
                    {#if entry.enabled}
                      <ToggleRight size={16} /> Disable
                    {:else}
                      <ToggleLeft size={16} /> Enable
                    {/if}
                  </button>
                  <button
                    type="button"
                    on:click={() => moveUp(index)}
                    disabled={index === 0}
                    class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    <ArrowUp size={16} /> Up
                  </button>
                  <button
                    type="button"
                    on:click={() => moveDown(index)}
                    disabled={index === entries.length - 1}
                    class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    <ArrowDown size={16} /> Down
                  </button>
                </div>
              </div>
            </div>
          {/each}
        </div>
      </div>

      <div class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
        <p class="text-sm text-slate-500">
          Drag and drop rows to reorder them, then save to update the backend priority list. Disabled methods remain in the list but will not be attempted.
        </p>
      </div>
    {/if}
  </div>
</div>
