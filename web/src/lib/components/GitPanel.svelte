<script lang="ts">
  import { X, FilePlus, FileMinus, FilePen, FileQuestion } from 'lucide-svelte';
  import { Badge, Button, Spinner } from './ui';
  import { toast } from '$lib/stores/toast';
  import {
    getGitChanges, getGitDiff, gitStage, gitCommit,
    type GitChange, type GitStatus
  } from '$lib/api/files';

  interface Props {
    serverId: number;
    path: string;
    open: boolean;
    status: GitStatus | null;
    onclose: () => void;
    onchanged: () => void; // after stage/commit so the page refreshes chip + files
  }

  let { serverId, path, open, status, onclose, onchanged }: Props = $props();

  let changes = $state<GitChange[]>([]);
  let loading = $state(false);
  let selected = $state<GitChange | null>(null);
  let diffText = $state('');
  let diffUntracked = $state(false);
  let loadingDiff = $state(false);
  let commitMsg = $state('');
  let committing = $state(false);

  // Re-fetch whenever the drawer opens or the browsed dir changes.
  $effect(() => {
    if (open && serverId && path) load();
  });

  async function load() {
    loading = true;
    try {
      changes = (await getGitChanges(serverId, path)) || [];
      if (selected && !changes.some((c) => c.path === selected!.path)) {
        selected = null;
        diffText = '';
        diffUntracked = false;
      }
    } catch (err: any) {
      toast.error('Failed to load git changes: ' + (err?.message || 'unknown'));
      changes = [];
    } finally {
      loading = false;
    }
  }

  async function showDiff(c: GitChange) {
    selected = c;
    loadingDiff = true;
    diffText = '';
    diffUntracked = false;
    try {
      const d = await getGitDiff(serverId, path, c.path);
      diffText = d.content || '';
      diffUntracked = !!d.untracked;
      if (!diffText && !diffUntracked) diffText = '(no changes)';
    } catch (err: any) {
      toast.error('Failed to load diff: ' + (err?.message || 'unknown'));
    } finally {
      loadingDiff = false;
    }
  }

  async function doStage(files: string[], unstage: boolean) {
    try {
      await gitStage(serverId, path, files, unstage);
      await load();
      onchanged();
    } catch (err: any) {
      toast.error('Git stage failed: ' + (err?.message || 'unknown'));
    }
  }

  async function doCommit() {
    committing = true;
    try {
      const st = await gitCommit(serverId, path, commitMsg.trim());
      commitMsg = '';
      selected = null;
      diffText = '';
      changes = [];
      onchanged();
      toast.success(`Committed on ${st.branch || st.head || 'HEAD'}`);
      await load();
    } catch (err: any) {
      toast.error('Commit failed: ' + (err?.message || 'unknown'));
    } finally {
      committing = false;
    }
  }

  function changeIcon(c: GitChange) {
    if (c.untracked) return FileQuestion;
    if (c.added) return FilePlus;
    if (c.deleted) return FileMinus;
    return FilePen;
  }

  function badgeFor(c: GitChange): { variant: 'success' | 'warning' | 'error' | 'info' | 'neutral'; label: string } {
    if (c.untracked) return { variant: 'neutral', label: '??' };
    if (c.deleted) return { variant: 'error', label: 'D' };
    if (c.renamed) return { variant: 'info', label: 'R' };
    if (c.added) return { variant: c.staged ? 'success' : 'info', label: 'A' };
    return { variant: c.staged ? 'success' : 'warning', label: c.staged ? 'M✓' : 'M' };
  }
</script>

{#if open}
  <!-- Backdrop -->
  <div
    class="fixed inset-0 z-40 bg-black/40 backdrop-blur-sm"
    onclick={onclose}
    role="presentation"
  ></div>

  <aside
    class="fixed right-0 top-0 z-50 flex h-full w-full max-w-md flex-col border-l border-border bg-surface shadow-2xl"
    role="dialog"
    aria-modal="true"
    aria-label="Git panel"
  >
    <!-- Header -->
    <div class="flex items-center justify-between border-b border-border px-4 py-3">
      <div class="min-w-0">
        <h2 class="text-sm font-semibold text-fg">
          Git
          {#if status?.isRepo}
            <span class="ml-1 font-normal text-fg-subtle">
              ⎇ {status.branch || status.head || 'detached'}
            </span>
          {/if}
        </h2>
        <p class="truncate text-xs text-fg-subtle">{path}</p>
      </div>
      <Button variant="ghost" size="sm" onclick={onclose}>
        <X size={16} />
      </Button>
    </div>

    <!-- Changes list -->
    <div class="flex-1 overflow-y-auto p-2">
      {#if loading}
        <div class="flex justify-center py-8"><Spinner size="sm" label="Loading changes" /></div>
      {:else if changes.length === 0}
        <p class="px-3 py-8 text-center text-xs text-fg-subtle">Working tree clean 🎉</p>
      {:else}
        {#each changes as c (c.path)}
          {@const Icon = changeIcon(c)}
          {@const b = badgeFor(c)}
          <button
            class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-surface-muted"
            class:bg-surface-muted={selected?.path === c.path}
            onclick={() => showDiff(c)}
          >
            <Icon size={14} class="shrink-0 text-fg-subtle" />
            <span class="min-w-0 flex-1 truncate font-mono" title={c.path}>{c.path}</span>
            <Badge variant={b.variant} size="sm">{b.label}</Badge>
          </button>
        {/each}

        <!-- Diff view for selected file -->
        {#if selected}
          <div class="mt-2 rounded-md border border-border p-2">
            <div class="mb-1 truncate font-mono text-xs font-medium text-fg">{selected.path}</div>
            {#if loadingDiff}
              <div class="flex justify-center py-4"><Spinner size="sm" label="Loading diff" /></div>
            {:else if diffUntracked}
              <pre class="max-h-64 overflow-auto whitespace-pre-wrap break-all text-[11px] leading-relaxed text-fg-subtle">{selected.path} is untracked — stage it to include it in a commit.</pre>
            {:else}
              <pre class="max-h-64 overflow-auto whitespace-pre-wrap break-all text-[11px] leading-relaxed">{diffText}</pre>
            {/if}
            <div class="mt-2 flex gap-2">
              {#if !selected.staged}
                <Button size="sm" onclick={() => doStage([selected!.path], false)}>Stage</Button>
              {:else}
                <Button size="sm" variant="secondary" onclick={() => doStage([selected!.path], true)}>Unstage</Button>
              {/if}
            </div>
          </div>
        {/if}

        <!-- Commit box -->
        {#if changes.some((c) => c.staged)}
          <div class="mt-3 border-t border-border pt-3">
            <textarea
              bind:value={commitMsg}
              placeholder="Commit message…"
              rows={2}
              class="w-full resize-none rounded-md border border-border bg-surface px-2 py-1.5 text-xs text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
            ></textarea>
            <Button size="sm" loading={committing} disabled={!commitMsg.trim()} onclick={doCommit}>
              Commit staged
            </Button>
          </div>
        {/if}
      {/if}
    </div>
  </aside>
{/if}
