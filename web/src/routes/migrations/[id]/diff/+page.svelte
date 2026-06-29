<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { ArrowLeft, ArrowRightLeft, Minus, Plus, Pencil } from 'lucide-svelte';
  import { migrationApi, type DiffResult, type MigrationPlan } from '$lib/api/migrations';
  import { Badge, Card, EmptyState, Spinner } from '$lib/components/ui';
  import { toast } from '$lib/stores/toast';

  type DiffKind = 'added' | 'removed' | 'changed';
  type DiffSection = {
    name: string;
    added: string[];
    removed: string[];
    changed: string[];
  };

  const migrationId = $derived(page.params.id);

  let migration = $state<MigrationPlan | null>(null);
  let diffData = $state<unknown>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  function isRecord(value: unknown): value is Record<string, unknown> {
    return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
  }

  function normalizeEntry(value: unknown): string {
    if (typeof value === 'string') return value;
    if (typeof value === 'number' || typeof value === 'boolean') return String(value);
    if (isRecord(value)) {
      return String(value.name ?? value.path ?? value.id ?? value.resource ?? value.key ?? JSON.stringify(value));
    }
    return String(value ?? '');
  }

  function toList(value: unknown): string[] {
    if (Array.isArray(value)) return value.map((item) => normalizeEntry(item));
    if (value === null || value === undefined) return [];
    if (isRecord(value) && Array.isArray(value.items)) return value.items.map((item) => normalizeEntry(item));
    return [normalizeEntry(value)];
  }

  function buildSection(name: string, source: Record<string, unknown>): DiffSection | null {
    const added = toList(source.added ?? source.onlyInTarget ?? source.created ?? source.new);
    const removed = toList(source.removed ?? source.onlyInSource ?? source.deleted ?? source.missing);
    const changed = toList(source.changed ?? source.different ?? source.modified ?? source.updated);

    if (!added.length && !removed.length && !changed.length) return null;
    return { name, added, removed, changed };
  }

  function buildSections(data: unknown): DiffSection[] {
    if (!isRecord(data)) return [];

    if (Array.isArray(data.categories)) {
      return data.categories
        .map((category, index) => {
          const record = isRecord(category) ? category : null;
          if (!record) return null;

          const section = buildSection(String(record.category ?? record.name ?? `Category ${index + 1}`), {
            added: record.onlyInTarget ?? record.added,
            removed: record.onlyInSource ?? record.removed,
            changed: record.different ?? record.changed,
          });

          return section;
        })
        .filter((section): section is DiffSection => section !== null);
    }

    const excludedKeys = new Set(['sourceId', 'targetId', 'summary', 'source', 'target', 'metadata', 'overview']);
    return Object.entries(data)
      .filter(([key, value]) => !excludedKeys.has(key) && value !== null && value !== undefined)
      .map(([name, value]) => {
        if (Array.isArray(value)) {
          return buildSection(name, { changed: value });
        }

        if (isRecord(value)) {
          const section = buildSection(name, value);
          if (section) return section;

          const nestedKeys = Object.entries(value).filter(([, nested]) => Array.isArray(nested) || isRecord(nested));
          const nested = nestedKeys.flatMap(([nestedName, nestedValue]) => {
            if (Array.isArray(nestedValue)) {
              const fromArray = buildSection(`${name} · ${nestedName}`, { changed: nestedValue });
              return fromArray ? [fromArray] : [];
            }

            if (isRecord(nestedValue)) {
              const nestedSection = buildSection(`${name} · ${nestedName}`, nestedValue);
              return nestedSection ? [nestedSection] : [];
            }

            return [];
          });

          return nested.length > 0 ? nested[0] : null;
        }

        return null;
      })
      .filter((section): section is DiffSection => section !== null);
  }

  function summarizeSections(sections: DiffSection[]): { added: number; removed: number; changed: number } {
    return sections.reduce(
      (acc, section) => ({
        added: acc.added + section.added.length,
        removed: acc.removed + section.removed.length,
        changed: acc.changed + section.changed.length,
      }),
      { added: 0, removed: 0, changed: 0 }
    );
  }

  function summarizeData(data: unknown, sections: DiffSection[]): { added: number; removed: number; changed: number } {
    if (isRecord(data)) {
      if (isRecord(data.summary)) {
        const summary = data.summary;
        const added = Number(summary.added ?? summary.addedCount ?? summary.totalAdded ?? summary.created ?? 0);
        const removed = Number(summary.removed ?? summary.removedCount ?? summary.totalRemoved ?? summary.deleted ?? 0);
        const changed = Number(summary.changed ?? summary.changedCount ?? summary.totalChanged ?? summary.modified ?? 0);

        if ([added, removed, changed].some((value) => Number.isFinite(value) && value > 0)) {
          return { added, removed, changed };
        }
      }

      const added = Number(data.added ?? data.addedCount ?? 0);
      const removed = Number(data.removed ?? data.removedCount ?? 0);
      const changed = Number(data.changed ?? data.changedCount ?? 0);

      if ([added, removed, changed].some((value) => Number.isFinite(value) && value > 0)) {
        return { added, removed, changed };
      }
    }

    return summarizeSections(sections);
  }

  function variantForKind(kind: DiffKind): 'success' | 'warning' | 'error' {
    if (kind === 'added') return 'success';
    if (kind === 'removed') return 'error';
    return 'warning';
  }

  const sections = $derived.by(() => buildSections(diffData));
  const summary = $derived.by(() => summarizeData(diffData, sections));

  async function loadDiff(): Promise<void> {
    loading = true;
    error = null;

    try {
      migration = await migrationApi.get(Number(migrationId));
      diffData = await migrationApi.diff(migration.sourceServerId, migration.targetServerId);
    } catch (err) {
      error = err instanceof Error ? err.message : 'Failed to load diff';
      toast.error(error);
    } finally {
      loading = false;
    }
  }

  function kindIcon(kind: DiffKind) {
    if (kind === 'added') return Plus;
    if (kind === 'removed') return Minus;
    return Pencil;
  }

  onMount(() => {
    void loadDiff();
  });
</script>

<svelte:head>
  <title>Server Diff</title>
</svelte:head>

<div class="p-4 sm:p-6 max-w-6xl mx-auto">
  <a href={migration ? `/migrations/${migration.id}` : `/migrations/${migrationId}`} class="mb-4 inline-flex items-center gap-2 text-sm text-slate-600 transition-colors hover:text-slate-900">
    <ArrowLeft size={16} />
    Back to migration
  </a>

  {#if loading}
    <div class="flex items-center gap-3 py-10 text-slate-500">
      <Spinner size="md" label="Loading diff" />
      <span>Loading server diff...</span>
    </div>
  {:else if error}
    <div class="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
      {error}
    </div>
  {:else if sections.length === 0}
    <EmptyState
      title="No differences found"
      description="The selected servers appear to be in sync."
      icon={emptyIcon}
    />
  {:else}
    <div class="space-y-6">
      <div class="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h1 class="text-2xl font-semibold text-slate-900">Server Diff</h1>
          <p class="mt-1 text-sm text-slate-500">
            {migration ? `${migration.sourceServerId} → ${migration.targetServerId}` : `Migration ${migrationId}`}
          </p>
        </div>

        <div class="flex flex-wrap items-center gap-2 text-sm">
          <Badge variant="success">{summary.added} added</Badge>
          <Badge variant="error">{summary.removed} removed</Badge>
          <Badge variant="warning">{summary.changed} changed</Badge>
        </div>
      </div>

      <Card padding="lg">
        <div class="flex flex-wrap items-center gap-3 text-sm text-slate-600">
          <div class="inline-flex items-center gap-2 rounded-lg bg-green-50 px-3 py-2 text-green-800">
            <Plus size={16} />
            {summary.added} added
          </div>
          <div class="inline-flex items-center gap-2 rounded-lg bg-red-50 px-3 py-2 text-red-800">
            <Minus size={16} />
            {summary.removed} removed
          </div>
          <div class="inline-flex items-center gap-2 rounded-lg bg-yellow-50 px-3 py-2 text-yellow-800">
            <Pencil size={16} />
            {summary.changed} changed
          </div>
        </div>
      </Card>

      <div class="space-y-4">
        {#each sections as section}
          <Card padding="lg">
            <div class="mb-4 flex items-center justify-between gap-3">
              <h2 class="text-lg font-semibold text-slate-900">{section.name}</h2>
              <div class="flex flex-wrap items-center gap-2 text-xs font-medium">
                {#if section.added.length > 0}
                  <Badge variant="success">{section.added.length} added</Badge>
                {/if}
                {#if section.removed.length > 0}
                  <Badge variant="error">{section.removed.length} removed</Badge>
                {/if}
                {#if section.changed.length > 0}
                  <Badge variant="warning">{section.changed.length} changed</Badge>
                {/if}
              </div>
            </div>

            <div class="grid gap-4 lg:grid-cols-3">
              {#if section.added.length > 0}
                <div class="rounded-lg border border-green-200 bg-green-50 p-4">
                  <div class="mb-3 flex items-center gap-2 text-sm font-semibold text-green-800">
                    <Plus size={16} />
                    Added
                  </div>
                  <div class="space-y-2">
                    {#each section.added as item}
                      <div class="rounded-lg border border-green-200 bg-white/80 px-3 py-2 text-sm text-green-800">
                        {item}
                      </div>
                    {/each}
                  </div>
                </div>
              {/if}

              {#if section.removed.length > 0}
                <div class="rounded-lg border border-red-200 bg-red-50 p-4">
                  <div class="mb-3 flex items-center gap-2 text-sm font-semibold text-red-800">
                    <Minus size={16} />
                    Removed
                  </div>
                  <div class="space-y-2">
                    {#each section.removed as item}
                      <div class="rounded-lg border border-red-200 bg-white/80 px-3 py-2 text-sm text-red-800">
                        {item}
                      </div>
                    {/each}
                  </div>
                </div>
              {/if}

              {#if section.changed.length > 0}
                <div class="rounded-lg border border-yellow-200 bg-yellow-50 p-4">
                  <div class="mb-3 flex items-center gap-2 text-sm font-semibold text-yellow-800">
                    <Pencil size={16} />
                    Changed
                  </div>
                  <div class="space-y-2">
                    {#each section.changed as item}
                      <div class="rounded-lg border border-yellow-200 bg-white/80 px-3 py-2 text-sm text-yellow-800">
                        {item}
                      </div>
                    {/each}
                  </div>
                </div>
              {/if}
            </div>
          </Card>
        {/each}
      </div>
    </div>
  {/if}
</div>

{#snippet emptyIcon()}
  <ArrowRightLeft size={22} />
{/snippet}
