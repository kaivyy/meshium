<script lang="ts">
  import type { Snippet } from 'svelte';
  import { ChevronDown, ChevronUp, Loader2 } from 'lucide-svelte';

  type SortDirection = 'asc' | 'desc';
  type Align = 'left' | 'center' | 'right';

  export interface DataTableColumn {
    key: string;
    label: string;
    sortable?: boolean;
    width?: string;
    align?: Align;
  }

  interface DataTableProps {
    columns: DataTableColumn[];
    data: Record<string, unknown>[];
    loading?: boolean;
    rowKey?: string;
    onRowClick?: (row: Record<string, unknown>) => void;
    empty?: Snippet;
    cell?: Snippet<[{ column: DataTableColumn; row: Record<string, unknown>; value: unknown }]>;
  }

  let {
    columns,
    data,
    loading = false,
    rowKey = 'id',
    onRowClick,
    empty,
    cell,
  }: DataTableProps = $props();

  let sortColumn = $state<string | null>(null);
  let sortDirection = $state<SortDirection>('asc');

  function getCellValue(row: Record<string, unknown>, key: string): unknown {
    return row[key];
  }

  function formatCellValue(value: unknown): string {
    if (value === null || value === undefined) return '—';
    if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
      return String(value);
    }
    return JSON.stringify(value);
  }

  function compareValues(a: unknown, b: unknown): number {
    if (typeof a === 'number' && typeof b === 'number') return a - b;
    return formatCellValue(a).toLowerCase().localeCompare(formatCellValue(b).toLowerCase());
  }

  function handleSort(column: DataTableColumn): void {
    if (!column.sortable) return;
    if (sortColumn === column.key) {
      sortDirection = sortDirection === 'asc' ? 'desc' : 'asc';
    } else {
      sortColumn = column.key;
      sortDirection = 'asc';
    }
  }

  const sortedData = $derived.by(() => {
    if (!sortColumn) return data;
    const key = sortColumn;
    const next = [...data];
    next.sort((a, b) => {
      const result = compareValues(getCellValue(a, key), getCellValue(b, key));
      return sortDirection === 'asc' ? result : -result;
    });
    return next;
  });

  function alignClass(align?: Align): string {
    return align === 'center' ? 'text-center' : align === 'right' ? 'text-right' : 'text-left';
  }
</script>

<div class="overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm">
  <table class="min-w-full divide-y divide-slate-200">
    <thead class="bg-slate-50">
      <tr>
        {#each columns as column}
          <th
            scope="col"
            class={`px-4 py-3 text-xs font-semibold uppercase tracking-wide text-slate-500 ${alignClass(column.align)}`}
            style={column.width ? `width: ${column.width};` : undefined}
          >
            {#if column.sortable}
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded-lg transition-colors hover:text-slate-900"
                onclick={() => handleSort(column)}
              >
                <span>{column.label}</span>
                {#if sortColumn === column.key}
                  {#if sortDirection === 'asc'}
                    <ChevronUp size={14} />
                  {:else}
                    <ChevronDown size={14} />
                  {/if}
                {/if}
              </button>
            {:else}
              <span>{column.label}</span>
            {/if}
          </th>
        {/each}
      </tr>
    </thead>

    <tbody class="divide-y divide-slate-100">
      {#if loading}
        <tr>
          <td class="px-4 py-8 text-center text-slate-500" colspan={columns.length}>
            <div class="flex items-center justify-center gap-2">
              <Loader2 size={18} class="animate-spin" />
              <span>Loading...</span>
            </div>
          </td>
        </tr>
      {:else if sortedData.length === 0}
        <tr>
          <td class="px-4 py-8 text-center text-sm text-slate-500" colspan={columns.length}>
            {#if empty}
              {@render empty()}
            {:else}
              No data
            {/if}
          </td>
        </tr>
      {:else}
        {#each sortedData as row (row[rowKey] as string | number)}
          <tr
            class={onRowClick ? 'cursor-pointer hover:bg-slate-50' : ''}
            role={onRowClick ? 'button' : undefined}
            tabindex={onRowClick ? 0 : undefined}
            onclick={() => onRowClick?.(row)}
            onkeydown={(e) => {
              if (onRowClick && (e.key === 'Enter' || e.key === ' ')) {
                e.preventDefault();
                onRowClick(row);
              }
            }}
          >
            {#each columns as column}
              {@const value = getCellValue(row, column.key)}
              <td class={`px-4 py-4 text-sm text-slate-700 ${alignClass(column.align)}`}>
                {#if cell}
                  {@render cell({ column, row, value })}
                {:else}
                  {formatCellValue(value)}
                {/if}
              </td>
            {/each}
          </tr>
        {/each}
      {/if}
    </tbody>
  </table>
</div>
