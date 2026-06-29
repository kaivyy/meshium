<script lang="ts">
  import { onMount } from 'svelte';
  import { migrationApi, type MigrationPlan } from '$lib/api/migrations';
  import { Plus, ArrowRight, ArrowRightLeft, Trash2 } from 'lucide-svelte';
  import { PageHeader, Skeleton } from '$lib/components/ui';
  import { toast } from '$lib/stores/toast';

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

    try {
      await migrationApi.delete(id);
      migrations = migrations.filter((m) => m.id !== id);
      toast.success('Migration deleted');
    } catch {
      toast.error('Failed to delete migration');
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

<div class="p-4 sm:p-6 max-w-4xl mx-auto">
  <PageHeader title="Migrations" subtitle="Manage and monitor your server migrations.">
    {#snippet actions()}
      <a href="/migrations/new" class="flex items-center gap-1 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 text-sm font-medium">
        <Plus size={16} /> New Migration
      </a>
    {/snippet}
  </PageHeader>

  {#if loading}
    <div class="space-y-3">
      {#each Array(3) as _, index}
        <div class="flex items-center justify-between gap-4 rounded-lg border border-slate-200 bg-white p-4">
          <div class="flex items-center gap-3">
            <Skeleton width="200px" />
          </div>
          <div class="flex items-center gap-3">
            <Skeleton width="80px" height="20px" rounded />
            <Skeleton width="100px" />
          </div>
        </div>
      {/each}
    </div>
  {:else if migrations.length === 0}
    <div class="flex flex-col items-center text-center rounded-2xl border border-dashed border-slate-300 bg-white px-6 py-12">
      <div class="inline-flex items-center justify-center rounded-full bg-slate-100 p-3 text-slate-400">
        <ArrowRightLeft size={24} />
      </div>
      <h3 class="mt-4 text-sm font-medium text-slate-900">No migrations yet</h3>
      <p class="mt-1 text-sm text-slate-500">Create your first migration to move services between servers.</p>
      <a href="/migrations/new" class="mt-4 inline-flex items-center justify-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700">
        <Plus size={18} /> New Migration
      </a>
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
