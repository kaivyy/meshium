<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { api } from '$lib/api/client';
  import { toast } from '$lib/stores/toast';
  import { wsPlan, type WSMessage, type PlanRequest } from '$lib/api/migrations';
  import type { Server } from '$lib/stores/servers';
  import { ArrowLeft, ArrowRight, Check, Package, FileCode, Settings, Users, Loader, Container, Database } from 'lucide-svelte';

  let step = 1;
  let servers: Server[] = [];
  let sourceServerId = 0;
  let targetServerId = 0;
  let selectedCategories: string[] = [];
  let configPaths = '';
  // Database category config. Defaults assume the DB runs ON the source host
  // (localhost from the source server's perspective); port flips per engine.
  let dbEngine = 'postgres';
  let dbHost = 'localhost';
  let dbPort = 5432;
  let dbUsername = '';
  let dbPassword = '';
  let dbName = '';
  const DB_DEFAULT_PORTS: Record<string, number> = {
    postgres: 5432,
    mysql: 3306,
    mongodb: 27017,
    redis: 6379,
  };
  let planning = false;
  let planMessages: WSMessage[] = [];
  let ws: WebSocket | null = null;
  // Elapsed timer for the Plan step so "Planning..." shows how long collection
  // has been running instead of a bare spinner. Not persisted: this wizard
  // doesn't survive a refresh (it resets to step 1), so cross-reload doesn't
  // apply.
  let planElapsed = 0;
  let planTimer: ReturnType<typeof setInterval> | null = null;
  // Derived plan outcome, computed from the messages we've received so far.
  // A message stream can contain progress lines long before it ends in either
  // success or failure, so the button label must reflect the actual outcome —
  // not merely "a message exists" (which previously mislabeled in-progress
  // plans as "Retry").
  $: planDone = planMessages.some(m => m.step === 'plan' && m.status === 'complete');
  $: planFailed = planMessages.some(m => m.status === 'error') && !planDone;

  // Reactive gate for the Next button. Declared as a top-level reactive
  // statement (not a function called in the template) so Svelte 5's legacy
  // mode tracks sourceServerId/targetServerId/selectedCategories as
  // dependencies and re-applies the `disabled` attribute to the DOM when they
  // change. Calling a function in disabled={!canProceed()} did not reliably
  // re-evaluate, leaving the button disabled even after a server was selected.
  $: canProceed =
    step === 1
      ? sourceServerId > 0
      : step === 2
        ? targetServerId > 0 && targetServerId !== sourceServerId
        : step === 3
          ? selectedCategories.length > 0
          : true;

  const categories = [
    { id: 'packages', label: 'Packages', icon: Package, desc: 'Installed packages (apt, dnf, pacman, etc.)' },
    { id: 'configs', label: 'Config Files', icon: FileCode, desc: 'Configuration files from /etc/ and custom paths' },
    { id: 'services', label: 'Services', icon: Settings, desc: 'Systemd enabled services' },
    { id: 'users', label: 'Users & Security', icon: Users, desc: 'Users, groups, cron jobs, firewall rules' },
    { id: 'docker', label: 'Docker', icon: Container, desc: 'Running containers, compose files, volumes, images' },
    { id: 'database', label: 'Databases', icon: Database, desc: 'PostgreSQL / MySQL / MongoDB / Redis dump & restore' },
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
    if (planTimer) clearInterval(planTimer);
  });

  function startPlanTimer() {
    if (planTimer) clearInterval(planTimer);
    planElapsed = 0;
    planTimer = setInterval(() => { planElapsed += 1; }, 1000);
  }

  function stopPlanTimer() {
    if (planTimer) { clearInterval(planTimer); planTimer = null; }
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

  function selectDbEngine(e: Event) {
    dbEngine = (e.currentTarget as HTMLSelectElement).value;
    // Reset to the engine's default port when switching — only if the user
    // hasn't deliberately overridden (we can't tell, so just reset; cheap).
    dbPort = DB_DEFAULT_PORTS[dbEngine] ?? 5432;
  }

  function startPlanning() {
    planning = true;
    planMessages = [];
    startPlanTimer();
    toast.info('Planning migration...');

    const req: PlanRequest = {
      sourceServerId,
      targetServerId,
      categories: selectedCategories,
      configPaths: configPaths ? configPaths.split('\n').map(p => p.trim()).filter(p => p) : undefined,
      databaseConfig: selectedCategories.includes('database')
        ? { engine: dbEngine, databaseName: dbName.trim(), username: dbUsername, password: dbPassword, host: dbHost, port: dbPort }
        : undefined,
    };

    ws = wsPlan(
      req,
      (msg: WSMessage) => {
        planMessages = [...planMessages, msg];
        if (msg.step === 'plan' && msg.status === 'complete') {
          planning = false;
          stopPlanTimer();
          toast.success('Migration plan created');
          // Extract migration ID from the message value (format: "migration_id:123")
          const match = msg.value?.match(/migration_id:(\d+)/);
          const newId = match ? match[1] : '';
          setTimeout(() => {
            if (newId) {
              goto(`/migrations/${newId}/pipeline`);
            } else {
              goto('/migrations');
            }
          }, 1000);
        }
      },
      () => {
        // WS closed. If we never got `complete`, the plan may still have been
        // committed server-side (CreateMigration runs before collection), so a
        // silent reset to "Create" would hide it and risk a duplicate. Tell the
        // user to check the list instead of pretending nothing happened.
        planning = false;
        stopPlanTimer();
        if (!planMessages.some(m => m.step === 'plan' && m.status === 'complete')) {
          toast.warning('Connection closed mid-plan. Check the migration list — the plan may already exist.');
        }
      },
      () => {
        planning = false;
        stopPlanTimer();
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
    <a href="/migrations" class="text-sm text-fg-muted hover:text-fg flex items-center gap-1">
      <ArrowLeft size={16} /> Back to Migrations
    </a>
  </div>

  <h1 class="text-2xl font-bold mb-2 text-fg">New Migration</h1>
  <p class="text-sm text-fg-subtle mb-6">Step {step} of 4</p>

  <!-- Progress bar -->
  <div class="flex items-center mb-8">
    {#each [1, 2, 3, 4] as s}
      <div class="flex items-center {s < 4 ? 'flex-1' : ''}">
        <div class="w-8 h-8 rounded-full flex items-center justify-center text-sm font-medium shrink-0
          {s < step ? 'bg-success text-accent-fg' : s === step ? 'bg-accent text-accent-fg' : 'bg-surface-muted text-fg-subtle'}">
          {#if s < step}
            <Check size={16} />
          {:else}
            {s}
          {/if}
        </div>
        {#if s < 4}
          <div class="h-0.5 flex-1 mx-2 {s < step ? 'bg-success' : 'bg-surface-muted'}"></div>
        {/if}
      </div>
    {/each}
  </div>

  <!-- Step 1: Select Source -->
  {#if step === 1}
    <div class="space-y-4">
      <h2 class="text-lg font-semibold text-fg">Select Source Server</h2>
      <p class="text-sm text-fg-subtle">Choose the server to migrate FROM.</p>
      <div class="space-y-2">
        {#each servers as s}
          <button
            on:click={() => sourceServerId = s.id}
            class="w-full text-left p-4 rounded-lg border transition-colors
              {sourceServerId === s.id ? 'border-accent bg-accent-subtle' : 'border-border hover:border-border-strong'}"
          >
            <div class="flex items-center justify-between">
              <div class="min-w-0">
                <p class="font-medium text-fg truncate">{s.name}</p>
                <p class="text-sm text-fg-subtle truncate">{s.host}:{s.port} · {s.username}</p>
              </div>
              {#if sourceServerId === s.id}
                <Check class="text-accent shrink-0" size={20} />
              {/if}
            </div>
          </button>
        {/each}
      </div>
    </div>

  <!-- Step 2: Select Target -->
  {:else if step === 2}
    <div class="space-y-4">
      <h2 class="text-lg font-semibold text-fg">Select Target Server</h2>
      <p class="text-sm text-fg-subtle">Choose the server to migrate TO. Must be different from source.</p>
      <div class="space-y-2">
        {#each servers.filter(s => s.id !== sourceServerId) as s}
          <button
            on:click={() => targetServerId = s.id}
            class="w-full text-left p-4 rounded-lg border transition-colors
              {targetServerId === s.id ? 'border-accent bg-accent-subtle' : 'border-border hover:border-border-strong'}"
          >
            <div class="flex items-center justify-between">
              <div class="min-w-0">
                <p class="font-medium text-fg truncate">{s.name}</p>
                <p class="text-sm text-fg-subtle truncate">{s.host}:{s.port} · {s.username}</p>
              </div>
              {#if targetServerId === s.id}
                <Check class="text-accent shrink-0" size={20} />
              {/if}
            </div>
          </button>
        {/each}
      </div>
    </div>

  <!-- Step 3: Choose Categories -->
  {:else if step === 3}
    <div class="space-y-4">
      <h2 class="text-lg font-semibold text-fg">Choose Categories to Migrate</h2>
      <p class="text-sm text-fg-subtle">Select what to migrate from source to target.</p>
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        {#each categories as cat}
          <button
            on:click={() => toggleCategory(cat.id)}
            class="p-4 rounded-lg border text-left transition-colors
              {selectedCategories.includes(cat.id) ? 'border-accent bg-accent-subtle' : 'border-border hover:border-border-strong'}"
          >
            <div class="flex items-start gap-3">
              <cat.icon size={20} class="text-fg-muted mt-0.5 shrink-0" />
              <div>
                <p class="font-medium text-fg">{cat.label}</p>
                <p class="text-xs text-fg-subtle mt-1">{cat.desc}</p>
              </div>
            </div>
          </button>
        {/each}
      </div>

      {#if selectedCategories.includes('configs')}
        <div class="mt-4">
          <label for="configPaths" class="text-sm font-medium text-fg block mb-1">Config Paths (optional)</label>
          <p class="text-xs text-fg-subtle mb-2">One path per line. Default: /etc/</p>
          <textarea
            id="configPaths"
            bind:value={configPaths}
            placeholder="/etc/nginx/&#10;/etc/systemd/system/"
            class="w-full p-2 border border-border rounded text-sm font-mono"
            rows="3"
          ></textarea>
        </div>
      {/if}

      {#if selectedCategories.includes('database')}
        <div class="mt-4 p-4 rounded-lg border border-border space-y-3">
          <div>
            <p class="text-sm font-medium text-fg">Database Migration</p>
            <p class="text-xs text-fg-subtle mt-1">
              Dumps the source DB and restores it on the target. Downtime = transfer time.
              Leave DB name empty to migrate all user databases.
            </p>
          </div>
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div>
              <label for="dbEngine" class="text-xs font-medium text-fg block mb-1">Engine</label>
              <select id="dbEngine" value={dbEngine} on:change={selectDbEngine} class="w-full p-2 border border-border rounded text-sm bg-surface">
                <option value="postgres">PostgreSQL</option>
                <option value="mysql">MySQL / MariaDB</option>
                <option value="mongodb">MongoDB</option>
                <option value="redis">Redis</option>
              </select>
            </div>
            <div>
              <label for="dbName" class="text-xs font-medium text-fg block mb-1">Database name (optional)</label>
              <input id="dbName" bind:value={dbName} placeholder="empty = all user DBs" class="w-full p-2 border border-border rounded text-sm font-mono bg-surface" />
            </div>
            <div>
              <label for="dbHost" class="text-xs font-medium text-fg block mb-1">Host</label>
              <input id="dbHost" bind:value={dbHost} class="w-full p-2 border border-border rounded text-sm font-mono bg-surface" />
            </div>
            <div>
              <label for="dbPort" class="text-xs font-medium text-fg block mb-1">Port</label>
              <input id="dbPort" type="number" bind:value={dbPort} class="w-full p-2 border border-border rounded text-sm font-mono bg-surface" />
            </div>
            <div>
              <label for="dbUsername" class="text-xs font-medium text-fg block mb-1">Username</label>
              <input id="dbUsername" bind:value={dbUsername} class="w-full p-2 border border-border rounded text-sm font-mono bg-surface" />
            </div>
            <div>
              <label for="dbPassword" class="text-xs font-medium text-fg block mb-1">Password</label>
              <input id="dbPassword" type="password" bind:value={dbPassword} class="w-full p-2 border border-border rounded text-sm font-mono bg-surface" />
            </div>
          </div>
          {#if dbEngine === 'redis'}
            <p class="text-xs text-fg-subtle">Redis uses an RDB snapshot (no per-DB name/username); name/username are ignored.</p>
          {/if}
        </div>
      {/if}
    </div>

  <!-- Step 4: Review & Plan -->
  {:else if step === 4}
    <div class="space-y-4">
      <h2 class="text-lg font-semibold text-fg">Review & Create Plan</h2>
      <div class="bg-surface rounded-lg border border-border p-4 space-y-3">
        <div>
          <p class="text-sm text-fg-subtle">Source</p>
          <p class="font-medium text-fg break-words">{getServerName(sourceServerId)}</p>
        </div>
        <div>
          <p class="text-sm text-fg-subtle">Target</p>
          <p class="font-medium text-fg break-words">{getServerName(targetServerId)}</p>
        </div>
        <div>
          <p class="text-sm text-fg-subtle">Categories</p>
          <div class="flex flex-wrap gap-2 mt-1">
            {#each selectedCategories as cat}
              <span class="px-2 py-1 bg-accent-subtle text-accent text-xs rounded">{cat}</span>
            {/each}
          </div>
        </div>
        {#if configPaths}
          <div>
            <p class="text-sm text-fg-subtle">Config Paths</p>
            <p class="font-mono text-xs text-fg-muted break-all">{configPaths}</p>
          </div>
        {/if}
        {#if selectedCategories.includes('database')}
          <div>
            <p class="text-sm text-fg-subtle">Database</p>
            <p class="font-mono text-xs text-fg-muted">
              {dbEngine}://{dbUsername ? '***' : '(no auth)'}@{dbHost}:{dbPort}{dbName ? '/' + dbName : ' (all user DBs)'}
            </p>
          </div>
        {/if}
      </div>

      {#if planMessages.length > 0}
        <div class="bg-surface-muted text-fg rounded-lg p-4 max-h-60 overflow-auto">
          <div class="space-y-1">
            {#each planMessages as msg}
              <div class="text-xs font-mono break-all">
                <span class={msg.status === 'error' ? 'text-error' : msg.status === 'success' || msg.status === 'complete' ? 'text-success' : 'text-fg-subtle'}>
                  [{msg.status}]
                </span>
                <span class="text-fg-muted">{msg.step}</span>
                {#if msg.value}
                  <span class="text-fg-subtle">→ {msg.value}</span>
                {/if}
                {#if msg.error}
                  <span class="text-error">→ {msg.error}</span>
                {/if}
              </div>
            {/each}
          </div>
        </div>
      {/if}

      {#if !planning}
        {#if planDone}
          <button
            type="button"
            disabled
            class="w-full px-4 py-3 bg-success/20 text-success rounded-lg font-medium cursor-default"
          >
            Plan created — redirecting…
          </button>
        {:else if planFailed}
          <button
            type="button"
            on:click={startPlanning}
            class="w-full px-4 py-3 bg-accent text-accent-fg rounded-lg font-medium hover:bg-accent-hover"
          >
            Retry Migration Plan
          </button>
        {:else}
          <button
            type="button"
            on:click={startPlanning}
            class="w-full px-4 py-3 bg-accent text-accent-fg rounded-lg font-medium hover:bg-accent-hover"
          >
            Create Migration Plan
          </button>
        {/if}
      {:else if planning}
        <div class="flex items-center justify-center gap-2 text-sm text-fg-subtle">
          <Loader size={16} class="animate-spin" />
          Planning… {Math.floor(planElapsed / 60)}:{String(planElapsed % 60).padStart(2, '0')}
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
        class="flex items-center gap-1 px-4 py-2 text-sm text-fg-muted hover:text-fg disabled:opacity-50"
      >
        <ArrowLeft size={16} /> Back
      </button>
      <button
        on:click={nextStep}
        disabled={!canProceed}
        class="flex items-center gap-1 px-4 py-2 bg-accent text-accent-fg rounded-lg text-sm hover:bg-accent-hover disabled:opacity-50"
      >
        Next <ArrowRight size={16} />
      </button>
    </div>
  {/if}
</div>
