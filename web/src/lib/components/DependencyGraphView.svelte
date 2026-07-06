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
      case 'reverse_proxy': return 'border-blue-500/50 bg-blue-500/10 text-blue-300';
      case 'database': return 'border-purple-500/50 bg-purple-500/10 text-purple-300';
      case 'cache': return 'border-red-500/50 bg-red-500/10 text-red-300';
      case 'queue': return 'border-yellow-500/50 bg-yellow-500/10 text-yellow-300';
      case 'worker': return 'border-orange-500/50 bg-orange-500/10 text-orange-300';
      default: return 'border-gray-600 bg-gray-800 text-gray-300';
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

<div class="bg-gray-900 rounded-xl p-4 border border-gray-800">
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
                  <div class="text-xs text-gray-500">:{node.port}</div>
                {/if}
              </div>
            </div>
          {/each}
        </div>
        {#if li < layers.length - 1}
          <div class="flex justify-center">
            <svg width="16" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-gray-600">
              <path d="M12 5v14M19 12l-7 7-7-7"/>
            </svg>
          </div>
        {/if}
      {/each}
    </div>
  {:else}
    <div class="text-center py-8 text-gray-500">
      <p class="text-sm">No dependency graph available.</p>
      <p class="text-xs mt-1">Run discovery and planner to see the dependency graph.</p>
    </div>
  {/if}
</div>
