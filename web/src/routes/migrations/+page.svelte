<script lang="ts">
  import { onMount } from 'svelte';
  import { migrationApi, type MigrationPlan } from '$lib/api/migrations';
  import { Plus, ArrowRight, Trash2, CheckCircle, XCircle, Clock, Loader } from 'lucide-svelte';

  let migrations: MigrationPlan[] = [];
  let loading = true;
  let error = '';
  let deletingId: number | null = null;

  onMount(async () => {
    try {
      migrations = await migrationApi.list();
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to load migrations';
    } finally {
      loading = false;
    }
  });

  async function deleteMigration(id: number) {
    if (deletingId !== null) return;
    if (!confirm('Delete this migration?')) return;

    deletingId = id;
    try {
      await migrationApi.delete(id);
      migrations = migrations.filter(m => m.id !== id);
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to delete migration';
    } finally {
      deletingId = null;
    }
  }

  function statusBadge(status: string): string {
    switch (status) {
      case 'completed': return 'bg-green-100 text-green-700';
      case 'failed': return 'bg-red-100 text-red-700';
      case 'running': return 'bg-blue-100 text-blue-700';
      case 'planned': return 'bg-slate-100 text-slate-700';
      case 'rolled_back': return 'bg-yellow-100 text-yellow-700';
      default: return 'bg-slate-100 text-slate-700';
    }
  }
</script>

<div class="p-6 max-w-4xl mx-auto">
  <div class="flex items-center justify-between mb-6">
    <h1 class="text-xl font-bold text-slate-900">Migrations</h1>
    <a
      href="/migrations/new"
      class="flex items-center gap-1 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 text-sm font-medium"
    >
      <Plus size={16} /> New Migration
    </a>
  </div>

  {#if error}
    <div class="mb-4 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{error}</div>
  {/if}

  {#if loading}
    <div class="rounded-lg border border-slate-200 bg-white px-4 py-3 text-slate-500">Loading...</div>
  {:else if migrations.length === 0 && !error}
    <div class="text-center py-12">
      <p class="text-slate-500 mb-4">No migrations yet</p>
      <a href="/migrations/new" class="text-blue-600 hover:underline">Create your first migration</a>
    </div>
  {:else if migrations.length > 0}
    <div class="space-y-2">
      {#each migrations as m}
        <div class="flex items-start justify-between gap-4 p-4 bg-white rounded-lg border border-slate-200 hover:border-slate-300 transition-colors">
          <a
            href="/migrations/{m.id}"
            class="min-w-0 flex-1"
          >
            <div class="flex items-center justify-between gap-3">
              <div class="flex items-center gap-3 min-w-0">
                <div class="flex items-center gap-2 min-w-0">
                  <span class="text-sm font-medium text-slate-900">Server #{m.sourceId}</span>
                  <ArrowRight size={14} class="text-slate-400" />
                  <span class="text-sm font-medium text-slate-900">Server #{m.targetId}</span>
                </div>
              </div>
              <div class="flex items-center gap-3 shrink-0">
                <span class="px-2 py-1 rounded-full text-xs font-medium {statusBadge(m.status)}">
                  {m.status}
                </span>
                <span class="text-xs text-slate-500">{m.createdAt}</span>
              </div>
            </div>
            <div class="flex flex-wrap gap-1 mt-2">
              {#each m.categories as cat}
                <span class="px-2 py-0.5 bg-slate-50 text-slate-600 text-xs rounded">{cat}</span>
              {/each}
            </div>
          </a>
          <button
            type="button"
            on:click={() => deleteMigration(m.id)}
            disabled={deletingId === m.id}
            class="mt-0.5 flex items-center justify-center text-slate-400 hover:text-red-500 disabled:cursor-not-allowed disabled:opacity-50"
            aria-label={`Delete migration ${m.id}`}
          >
            {#if deletingId === m.id}
              <Loader size={16} class="animate-spin" />
            {:else}
              <Trash2 size={16} />
            {/if}
          </button>
        </div>
      {/each}
    </div>
  {/if}
</div>
