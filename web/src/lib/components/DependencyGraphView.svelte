<script lang="ts">
  import type { DependencyGraph, DependencyNode } from '$lib/api/pipeline';

  export let graph: DependencyGraph | null;

  function nodeIcon(type: string): string {
    switch (type) {
      case 'reverse_proxy': return '🌐';
      case 'stateless_application':
      case 'stateful_application': return '📦';
      case 'database': return '🗄️';
      case 'cache': return '⚡';
      case 'queue': return '📋';
      case 'worker': return '⚙️';
      default: return '🔷';
    }
  }

  function nodeColor(type: string): string {
    switch (type) {
      case 'reverse_proxy': return 'border-info/50 bg-info/10 text-info';
      case 'database': return 'border-accent/50 bg-accent/10 text-accent';
      case 'cache': return 'border-error/50 bg-error/10 text-error';
      case 'queue': return 'border-warning/50 bg-warning/10 text-warning';
      case 'worker': return 'border-success/50 bg-success/10 text-success';
      default: return 'border-border bg-surface-muted text-fg-muted';
    }
  }

  // Build a simple layered layout from the graph
  $: layers = graph ? buildLayers(graph) : [];

  function buildLayers(g: DependencyGraph): DependencyNode[][] {
    const nodeMap = new Map(g.nodes.map((n) => [n.id, n]));
    const inDegree = new Map(g.nodes.map((n) => [n.id, 0]));
    for (const edge of g.edges) {
      inDegree.set(edge.to, (inDegree.get(edge.to) || 0) + 1);
    }
    // Simple topological sort
    const result: DependencyNode[][] = [];
    const visited = new Set<string>();
    let current = g.nodes.filter((n) => (inDegree.get(n.id) || 0) === 0);
    while (current.length > 0) {
      result.push(current);
      for (const node of current) visited.add(node.id);
      const next: DependencyNode[] = [];
      for (const edge of g.edges) {
        if (visited.has(edge.from) && !visited.has(edge.to) && !next.find((n) => n.id === edge.to)) {
          const node = nodeMap.get(edge.to);
          if (node) next.push(node);
        }
      }
      current = next;
    }
    return result;
  }
</script>

<div class="bg-surface rounded-xl p-4 border border-border">
  {#if graph && graph.nodes.length > 0}
    <div class="space-y-3">
      {#each layers as layer, li}
        <div class="flex items-center justify-center gap-3 flex-wrap">
          {#each layer as node}
            <div class="flex items-center gap-2 px-3 py-2 rounded-lg border {nodeColor(node.workloadType || node.type)}">
              <span class="text-base">{nodeIcon(node.workloadType || node.type)}</span>
              <div>
                <div class="text-sm font-medium">{node.name}</div>
                {#if node.port}
                  <div class="text-xs text-fg-subtle">:{node.port}</div>
                {/if}
              </div>
            </div>
          {/each}
        </div>
        {#if li < layers.length - 1}
          <div class="flex justify-center">
            <svg width="16" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-fg-subtle">
              <path d="M12 5v14M19 12l-7 7-7-7"/>
            </svg>
          </div>
        {/if}
      {/each}
    </div>
  {:else}
    <div class="text-center py-8 text-fg-subtle">
      <p class="text-sm">No dependency graph available.</p>
      <p class="text-xs mt-1">Run discovery and planner to see the dependency graph.</p>
    </div>
  {/if}
</div>
