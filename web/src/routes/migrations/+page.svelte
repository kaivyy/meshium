<script lang="ts">
  import { onMount } from 'svelte';
  import { migrationApi, type MigrationPlan } from '$lib/api/migrations';
  import { Plus, ArrowRight, Trash2, CheckCircle, XCircle, Clock, Loader } from 'lucide-svelte';

  let migrations: MigrationPlan[] = [];
  let loading = true;

  onMount(async () => {
    try {
      migrations = await migrationApi.list();
    } catch {
      // handle error
    } finally {
      loading = false;
    }
  });

  async function deleteMigration(id: number, event: MouseEvent) {
    event.stopPropagation();
    if (!confirm('Delete this migration?')) return;
    await migrationApi.delete(id);
    migrations = migrations.filter(m => m.id !== id);
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

<div class="p-4 sm:p-6 max-w-4xl mx-auto">
  <div class="flex items-center justify-between mb-6">
    <h1 class="text-xl font-bold text-slate-900">Migrations</h1>
    <a
      href="/migrations/new"
      class="flex items-center gap-1 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 text-sm font-medium"
    >
      <Plus size={16} /> New Migration
    </a>
  </div>

  {#if loading}
    <p class="text-slate-500">Loading...</p>
  {:else if migrations.length === 0}
    <div class="text-center py-12">
      <p class="text-slate-500 mb-4">No migrations yet</p>
      <a href="/migrations/new" class="text-blue-600 hover:underline">Create your first migration</a>
    </div>
  {:else}
    <div class="space-y-2">
      {#each migrations as m}
        <a
          href="/migrations/{m.id}"
          class="block p-4 bg-white rounded-lg border border-slate-200 hover:border-slate-300 transition-colors"
        >
          <div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <div class="flex items-center gap-3">
              <div class="flex items-center gap-2">
                <span class="text-sm font-medium text-slate-900">Server #{m.sourceServerId}</span>
                <ArrowRight size={14} class="text-slate-400" />
                <span class="text-sm font-medium text-slate-900">Server #{m.targetServerId}</span>
              </div>
            </div>
            <div class="flex items-center gap-3">
              <span class="px-2 py-1 rounded-full text-xs font-medium {statusBadge(m.status)}">
                {m.status}
              </span>
              <span class="text-xs text-slate-500">{m.createdAt}</span>
              <button
                on:click={(e) => deleteMigration(m.id, e)}
                class="text-slate-400 hover:text-red-500"
              >
                <Trash2 size={16} />
              </button>
            </div>
          </div>
          <div class="flex flex-wrap gap-1 mt-2">
            {#each m.categories as cat}
              <span class="px-2 py-0.5 bg-slate-50 text-slate-600 text-xs rounded">{cat}</span>
            {/each}
          </div>
        </a>
      {/each}
    </div>
  {/if}
</div>
