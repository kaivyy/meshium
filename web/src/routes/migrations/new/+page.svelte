<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { api } from '$lib/api/client';
  import { toast } from '$lib/stores/toast';
  import { wsPlan, type WSMessage, type PlanRequest } from '$lib/api/migrations';
  import type { Server } from '$lib/stores/servers';
  import { ArrowLeft, ArrowRight, Check, Package, FileCode, Settings, Users, Loader, Container } from 'lucide-svelte';

  let step = 1;
  let servers: Server[] = [];
  let sourceServerId = 0;
  let targetServerId = 0;
  let selectedCategories: string[] = [];
  let configPaths = '';
  let planning = false;
  let planMessages: WSMessage[] = [];
  let ws: WebSocket | null = null;

  const categories = [
    { id: 'packages', label: 'Packages', icon: Package, desc: 'Installed packages (apt, dnf, pacman, etc.)' },
    { id: 'configs', label: 'Config Files', icon: FileCode, desc: 'Configuration files from /etc/ and custom paths' },
    { id: 'services', label: 'Services', icon: Settings, desc: 'Systemd enabled services' },
    { id: 'users', label: 'Users & Security', icon: Users, desc: 'Users, groups, cron jobs, firewall rules' },
    { id: 'docker', label: 'Docker', icon: Container, desc: 'Running containers, compose files, volumes, images' },
  ];

  onMount(async () => {
    try {
      servers = await api.get('/servers') as Server[];

      const sourceParam = $page.url.searchParams.get('source');
      const targetParam = $page.url.searchParams.get('target');

      if (sourceParam) {
        sourceServerId = Number(sourceParam);
        step = 2;
      }

      if (targetParam) {
        targetServerId = Number(targetParam);
      }

      if (sourceParam && targetParam) {
        step = 3;
      }
    } catch {
      // handle error
    }
  });

  onDestroy(() => {
    ws?.close();
  });

  function canProceed(): boolean {
    switch (step) {
      case 1: return sourceServerId > 0;
      case 2: return targetServerId > 0 && targetServerId !== sourceServerId;
      case 3: return selectedCategories.length > 0;
      default: return true;
    }
  }

  function nextStep() {
    if (step < 4) step++;
  }

  function prevStep() {
    if (step > 1) step--;
  }

  function toggleCategory(id: string) {
    if (selectedCategories.includes(id)) {
      selectedCategories = selectedCategories.filter(c => c !== id);
    } else {
      selectedCategories = [...selectedCategories, id];
    }
  }

  function startPlanning() {
    planning = true;
    planMessages = [];
    toast.info('Planning migration...');

    const req: PlanRequest = {
      sourceServerId,
      targetServerId,
      categories: selectedCategories,
      configPaths: configPaths ? configPaths.split('\n').map(p => p.trim()).filter(p => p) : undefined,
    };

    ws = wsPlan(
      req,
      (msg: WSMessage) => {
        planMessages = [...planMessages, msg];
        if (msg.step === 'plan' && msg.status === 'complete') {
          planning = false;
          toast.success('Migration plan created');
          setTimeout(() => goto('/migrations'), 1000);
        }
      },
      () => { planning = false; },
      () => {
        planning = false;
        toast.error('Migration planning failed');
      }
    );
  }

  function getServerName(id: number): string {
    const s = servers.find(s => s.id === id);
    return s ? `${s.name} (${s.host})` : '';
  }
</script>

<div class="p-4 sm:p-6 max-w-3xl mx-auto">
  <div class="flex items-center gap-2 mb-6">
    <a href="/migrations" class="text-sm text-slate-600 hover:text-slate-900 flex items-center gap-1">
      <ArrowLeft size={16} /> Back to Migrations
    </a>
  </div>

  <h1 class="text-2xl font-bold mb-2 text-slate-900">New Migration</h1>
  <p class="text-sm text-slate-500 mb-6">Step {step} of 4</p>

  <!-- Progress bar -->
  <div class="flex items-center mb-8">
    {#each [1, 2, 3, 4] as s}
      <div class="flex items-center {s < 4 ? 'flex-1' : ''}">
        <div class="w-8 h-8 rounded-full flex items-center justify-center text-sm font-medium shrink-0
          {s < step ? 'bg-green-500 text-white' : s === step ? 'bg-blue-600 text-white' : 'bg-slate-200 text-slate-500'}">
          {#if s < step}
            <Check size={16} />
          {:else}
            {s}
          {/if}
        </div>
        {#if s < 4}
          <div class="h-0.5 flex-1 mx-2 {s < step ? 'bg-green-500' : 'bg-slate-200'}"></div>
        {/if}
      </div>
    {/each}
  </div>

  <!-- Step 1: Select Source -->
  {#if step === 1}
    <div class="space-y-4">
      <h2 class="text-lg font-semibold text-slate-900">Select Source Server</h2>
      <p class="text-sm text-slate-500">Choose the server to migrate FROM.</p>
      <div class="space-y-2">
        {#each servers as s}
          <button
            on:click={() => sourceServerId = s.id}
            class="w-full text-left p-4 rounded-lg border transition-colors
              {sourceServerId === s.id ? 'border-blue-500 bg-blue-50' : 'border-slate-200 hover:border-slate-300'}"
          >
            <div class="flex items-center justify-between">
              <div class="min-w-0">
                <p class="font-medium text-slate-900 truncate">{s.name}</p>
                <p class="text-sm text-slate-500 truncate">{s.host}:{s.port} · {s.username}</p>
              </div>
              {#if sourceServerId === s.id}
                <Check class="text-blue-600 shrink-0" size={20} />
              {/if}
            </div>
          </button>
        {/each}
      </div>
    </div>

  <!-- Step 2: Select Target -->
  {:else if step === 2}
    <div class="space-y-4">
      <h2 class="text-lg font-semibold text-slate-900">Select Target Server</h2>
      <p class="text-sm text-slate-500">Choose the server to migrate TO. Must be different from source.</p>
      <div class="space-y-2">
        {#each servers.filter(s => s.id !== sourceServerId) as s}
          <button
            on:click={() => targetServerId = s.id}
            class="w-full text-left p-4 rounded-lg border transition-colors
              {targetServerId === s.id ? 'border-blue-500 bg-blue-50' : 'border-slate-200 hover:border-slate-300'}"
          >
            <div class="flex items-center justify-between">
              <div class="min-w-0">
                <p class="font-medium text-slate-900 truncate">{s.name}</p>
                <p class="text-sm text-slate-500 truncate">{s.host}:{s.port} · {s.username}</p>
              </div>
              {#if targetServerId === s.id}
                <Check class="text-blue-600 shrink-0" size={20} />
              {/if}
            </div>
          </button>
        {/each}
      </div>
    </div>

  <!-- Step 3: Choose Categories -->
  {:else if step === 3}
    <div class="space-y-4">
      <h2 class="text-lg font-semibold text-slate-900">Choose Categories to Migrate</h2>
      <p class="text-sm text-slate-500">Select what to migrate from source to target.</p>
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        {#each categories as cat}
          <button
            on:click={() => toggleCategory(cat.id)}
            class="p-4 rounded-lg border text-left transition-colors
              {selectedCategories.includes(cat.id) ? 'border-blue-500 bg-blue-50' : 'border-slate-200 hover:border-slate-300'}"
          >
            <div class="flex items-start gap-3">
              <cat.icon size={20} class="text-slate-600 mt-0.5 shrink-0" />
              <div>
                <p class="font-medium text-slate-900">{cat.label}</p>
                <p class="text-xs text-slate-500 mt-1">{cat.desc}</p>
              </div>
            </div>
          </button>
        {/each}
      </div>

      {#if selectedCategories.includes('configs')}
        <div class="mt-4">
          <label for="configPaths" class="text-sm font-medium text-slate-900 block mb-1">Config Paths (optional)</label>
          <p class="text-xs text-slate-500 mb-2">One path per line. Default: /etc/</p>
          <textarea
            id="configPaths"
            bind:value={configPaths}
            placeholder="/etc/nginx/&#10;/etc/systemd/system/"
            class="w-full p-2 border border-slate-200 rounded text-sm font-mono"
            rows="3"
          ></textarea>
        </div>
      {/if}
    </div>

  <!-- Step 4: Review & Plan -->
  {:else if step === 4}
    <div class="space-y-4">
      <h2 class="text-lg font-semibold text-slate-900">Review & Create Plan</h2>
      <div class="bg-white rounded-lg border border-slate-200 p-4 space-y-3">
        <div>
          <p class="text-sm text-slate-500">Source</p>
          <p class="font-medium text-slate-900 break-words">{getServerName(sourceServerId)}</p>
        </div>
        <div>
          <p class="text-sm text-slate-500">Target</p>
          <p class="font-medium text-slate-900 break-words">{getServerName(targetServerId)}</p>
        </div>
        <div>
          <p class="text-sm text-slate-500">Categories</p>
          <div class="flex flex-wrap gap-2 mt-1">
            {#each selectedCategories as cat}
              <span class="px-2 py-1 bg-blue-50 text-blue-700 text-xs rounded">{cat}</span>
            {/each}
          </div>
        </div>
        {#if configPaths}
          <div>
            <p class="text-sm text-slate-500">Config Paths</p>
            <p class="font-mono text-xs text-slate-700 break-all">{configPaths}</p>
          </div>
        {/if}
      </div>

      {#if planMessages.length > 0}
        <div class="bg-slate-900 text-slate-100 rounded-lg p-4 max-h-60 overflow-auto">
          <div class="space-y-1">
            {#each planMessages as msg}
              <div class="text-xs font-mono break-all">
                <span class={msg.status === 'error' ? 'text-red-400' : msg.status === 'success' || msg.status === 'complete' ? 'text-green-400' : 'text-slate-400'}>
                  [{msg.status}]
                </span>
                <span class="text-slate-300">{msg.step}</span>
                {#if msg.value}
                  <span class="text-slate-500">→ {msg.value}</span>
                {/if}
                {#if msg.error}
                  <span class="text-red-400">→ {msg.error}</span>
                {/if}
              </div>
            {/each}
          </div>
        </div>
      {/if}

      {#if !planning && planMessages.length === 0}
        <button
          on:click={startPlanning}
          class="w-full px-4 py-3 bg-blue-600 text-white rounded-lg font-medium hover:bg-blue-700"
        >
          Create Migration Plan
        </button>
      {:else if planning}
        <div class="flex items-center justify-center gap-2 text-sm text-slate-500">
          <Loader size={16} class="animate-spin" />
          Planning...
        </div>
      {/if}
    </div>
  {/if}

  <!-- Navigation buttons -->
  {#if step < 4 && !planning}
    <div class="flex justify-between mt-8">
      <button
        on:click={prevStep}
        disabled={step === 1}
        class="flex items-center gap-1 px-4 py-2 text-sm text-slate-600 hover:text-slate-900 disabled:opacity-50"
      >
        <ArrowLeft size={16} /> Back
      </button>
      <button
        on:click={nextStep}
        disabled={!canProceed()}
        class="flex items-center gap-1 px-4 py-2 bg-blue-600 text-white rounded-lg text-sm hover:bg-blue-700 disabled:opacity-50"
      >
        Next <ArrowRight size={16} />
      </button>
    </div>
  {/if}
</div>
