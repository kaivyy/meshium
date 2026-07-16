<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { api } from '$lib/api/client';
  import { toast } from '$lib/stores/toast';
  import { wsPlan, decideReconcile, type WSMessage, type PlanRequest } from '$lib/api/migrations';
  import { pipelineApi, type PolicyMatrix } from '$lib/api/pipeline';
  import {
    canAutoCutover,
    engineSupport,
    trafficProviderSupport,
    dbEngineResume,
    dbEngineDowntime,
    dbEngineTransferNote
  } from '$lib/support-status';
  import SupportStatusBadge from '$lib/components/SupportStatusBadge.svelte';
  import { formatBytes } from '$lib/utils/format';
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
  let dbContainer = ''; // empty = host; else `docker exec -i <name> --` the engine
  // Phase 5E (E): execution location + migration mode drive honest capability
  // disclosure. execMode: host | container | compose. migrationMode:
  // snapshot_copy | live_replication (disabled while not genuinely wired).
  let dbExecMode = 'host';
  let dbComposeService = '';
  let dbComposeFile = '';
  let dbMigrationMode = 'snapshot_copy'; // live_replication unavailable → shown disabled
  // Traffic provider + automatic-cutover selection (Phase 4G, slice 3A). The
  // backend's /api/pipeline/policy is authoritative for what is selectable as
  // automatic; this UI only reflects + gates on it. Default provider is empty
  // (manual) so the shipped default is never automatic.
  let trafficProvider = '';
  let autoCutover = false;
  let policy: PolicyMatrix | null = null;
  let policyError = '';
  let configureError = '';
  const DB_DEFAULT_PORTS: Record<string, number> = {
    postgres: 5432,
    mysql: 3306,
    mongodb: 27017,
    redis: 6379,
  };
  let planning = false;
  let planMessages: WSMessage[] = [];
  let ws: WebSocket | null = null;
  // phase-4g1 truthful plan state machine. The wizard never hangs on a bare
  // spinner: completion = backend-confirmed (REST reconcile), not "I saw a WS
  // frame". States: idle | connecting | collecting | checking | completed |
  // failed | unknown.
  type PlanState = 'idle' | 'connecting' | 'collecting' | 'checking' | 'completed' | 'failed' | 'unknown';
  let planState: PlanState = 'idle';
  let recoveredMigrationId: number | null = null;
  let recoveredStatus: string | null = null;
  // Client-generated idempotency key for this create-plan attempt. Persisted to
  // the URL + sessionStorage BEFORE the first submit so a refresh/reconnect can
  // reconcile to the same backend migration instead of creating a duplicate.
  const OP_STORAGE_KEY = 'meshium_plan_op';
  function loadOperationId(): string {
    try {
      const fromUrl = $page.url.searchParams.get('op');
      if (fromUrl) return fromUrl;
      const fromStore = sessionStorage.getItem(OP_STORAGE_KEY);
      if (fromStore) return fromStore;
    } catch { /* ignore */ }
    return '';
  }
  function persistOperationId(op: string) {
    try {
      sessionStorage.setItem(OP_STORAGE_KEY, op);
      const url = new URL($page.url);
      url.searchParams.set('op', op);
      history.replaceState(null, '', url);
    } catch { /* ignore */ }
  }
  function clearOperationId() {
    try { sessionStorage.removeItem(OP_STORAGE_KEY); } catch { /* ignore */ }
    try {
      const url = new URL($page.url);
      url.searchParams.delete('op');
      history.replaceState(null, '', url);
    } catch { /* ignore */ }
  }
  function newOperationId(): string {
    // crypto.randomUUID is available in all targets we ship; fall back cheaply.
    try { return crypto.randomUUID(); } catch { return `op-${Date.now()}-${Math.random().toString(36).slice(2)}`; }
  }
  let operationId = '';
  // Elapsed timer for the Plan step so "Planning…" shows how long collection has
  // been running instead of a bare spinner.
  let planElapsed = 0;
  let planTimer: ReturnType<typeof setInterval> | null = null;
  // Completed when the backend has CONFIRMED the migration exists. We require a
  // `migration_id:N` terminal frame (id-bearing) — the older id-less
  // "Migration plan created" frame is non-terminal (demoted to progress) so the
  // FE never stops on a frame that carries no id.
  $: planDone = planMessages.some(m => m.step === 'plan' && m.status === 'complete' && /migration_id:\d+/.test(m.value ?? ''));
  $: planFailed = planMessages.some(m => m.status === 'error') && !planDone && planState !== 'completed';
  // In-flight = a create-plan operation is active or being reconciled. While
  // in-flight the submit button is disabled and double-submit is impossible.
  $: planInFlight = planning || planState === 'connecting' || planState === 'collecting' || planState === 'checking';

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
      // Phase 4G: fetch the authoritative support matrix. If this fails we still
      // plan (manual default), but cannot offer automatic — degrade honestly.
      try {
        policy = await pipelineApi.getPolicy();
      } catch (e) {
        policyError = e instanceof Error ? e.message : 'Failed to load support policy';
      }

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

      // phase-4g1 recovery: if we hold a create-plan operation id (URL or
      // sessionStorage), a plan was in flight or completed before this load. We
      // must NOT silently reset to Step 1 — restore Step 4 in a "checking"
      // state and reconcile against the backend, which is authoritative.
      const op = loadOperationId();
      if (op) {
        operationId = op;
        step = 4;
        planState = 'checking';
        await reconcilePlan(op);
      }
    } catch {
      // handle error
    }
  });

  // Prevent a stale opt-in: if the current engine+provider is not permitted for
  // automatic cutover, clear the checkbox. The backend still enforces at the
  // configure boundary; this is honest UI feedback, not authorization.
  $: autoCutoverAllowed = canAutoCutover(dbEngine, trafficProvider, policy).allowed;
  $: if (autoCutover && !autoCutoverAllowed) autoCutover = false;

  // Phase 5E (J): best-effort total data size from the collect frames. 0 = unknown
  // → the UI shows "size unknown", never "0 MB".
  $: dbEstimatedBytes = selectedCategories.includes('database')
    ? planMessages
        .filter(m => m.step === 'plan:database' && m.status === 'success')
        .reduce((sum, m) => sum + (m.estimatedBytes ?? 0), 0)
    : 0;

  onDestroy(() => {
    ws?.close();
    if (planTimer) clearInterval(planTimer);
  });

  function startPlanTimer() {
    if (planTimer) clearInterval(planTimer);
    planElapsed = 0;
    planTimer = setInterval(() => { planElapsed += 1; }, 1000);
    // Safety net: if collection runs past the server-side collect deadline
    // without a terminal frame OR a socket close, the backend has wedged (a
    // category collector can hang). The authoritative resolution is REST
    // reconcile — never an endless spinner. reconcilePlan no-ops if a terminal
    // frame arrived in the meantime (planState === 'completed').
    setTimeout(() => {
      if (planning && planState !== 'completed') {
        toast.error('Planning is taking unusually long — checking status…');
        reconcilePlan(operationId);
      }
    }, 6 * 60 * 1000);
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

  // Reconcile a pending create-plan operation against the backend, which is
  // authoritative. Called on load (refresh) and after a WS close. Never trusts a
  // WS frame alone — the decision is made by decideReconcile (unit-tested).
  async function reconcilePlan(op: string) {
    planState = 'checking';
    try {
      const list = await api.get(`/migrations?operationId=${encodeURIComponent(op)}`) as Array<{ id: number; status: string }>;
      const outcome = decideReconcile(list);
      if (outcome.kind === 'completed') {
        recoveredMigrationId = outcome.id;
        recoveredStatus = outcome.migrationStatus;
        planState = 'completed';
        planning = false;
        stopPlanTimer();
        clearOperationId();
        setTimeout(() => goto(`/migrations/${outcome.id}/pipeline`), 800);
        return;
      }
      if (outcome.kind === 'failed') {
        recoveredMigrationId = outcome.id;
        recoveredStatus = outcome.migrationStatus;
        planState = 'failed';
        planning = false;
        stopPlanTimer();
        return;
      }
      // unknown: nothing found / unreconcilable. Surface ambiguity, never a bare
      // spinner — and only once no live connection is keeping us in-flight.
      if (!planning) planState = 'unknown';
    } catch {
      // Reconciliation itself failed. Do not fake success or loop forever:
      // show the honest unknown state with guidance to check history.
      if (!planning) planState = 'unknown';
    }
  }

  // Confirm completion after the id-bearing terminal frame: persist the
  // provider/cutover choice (backend is authoritative and may return 400), then
  // navigate. id AND op are known here.
  async function onPlanCompleted(newId: number, op: string) {
    planning = false;
    stopPlanTimer();
    planState = 'completed';
    recoveredMigrationId = newId;
    clearOperationId();
    toast.success('Migration plan created');
    if (newId) {
      configureError = '';
      await pipelineApi
        .configure(Number(newId), {
          trafficProvider: trafficProvider || undefined,
          autoCutover: autoCutoverAllowed ? autoCutover : false,
        })
        .catch((e) => {
          configureError = e instanceof Error ? e.message : 'Failed to apply cutover configuration';
        });
    }
    setTimeout(() => {
      goto(newId ? `/migrations/${newId}/pipeline` : '/migrations');
    }, 1000);
  }

  function startPlanning() {
    if (planning || planState === 'checking') return; // double-submit guard
    // Generate + persist the operation id BEFORE opening the socket, so a
    // refresh/reconnect reconciles to the same backend migration.
    operationId = loadOperationId() || newOperationId();
    persistOperationId(operationId);

    planning = true;
    planState = 'connecting';
    planMessages = [];
    startPlanTimer();
    toast.info('Planning migration...');

    const req: PlanRequest = {
      sourceServerId,
      targetServerId,
      categories: selectedCategories,
      configPaths: configPaths ? configPaths.split('\n').map(p => p.trim()).filter(p => p) : undefined,
      databaseConfig: selectedCategories.includes('database')
        ? {
            engine: dbEngine,
            databaseName: dbName.trim(),
            username: dbUsername,
            password: dbPassword,
            host: dbHost,
            port: dbPort,
            container: dbExecMode === 'container' ? dbContainer.trim() : '',
            execMode: dbExecMode,
            composeService: dbExecMode === 'compose' ? dbComposeService.trim() : '',
            composeFile: dbExecMode === 'compose' ? dbComposeFile.trim() : '',
            migrationMode: dbMigrationMode,
          }
        : undefined,
      operationId,
    };

    ws = wsPlan(
      req,
      (msg: WSMessage) => {
        planMessages = [...planMessages, msg];
        if (msg.status === 'progress' || msg.status === 'success') planState = 'collecting';
        // Only a frame carrying `migration_id:N` is terminal. (The id-less
        // "Migration plan created" progress frame is never terminal.)
        if (msg.step === 'plan' && msg.status === 'complete' && /migration_id:(\d+)/.test(msg.value ?? '')) {
          const match = msg.value?.match(/migration_id:(\d+)/);
          const newId = match ? Number(match[1]) : 0;
          onPlanCompleted(newId, operationId);
        }
      },
      () => {
        // WS closed. The plan may have been committed server-side (CreateMigration
        // runs before collection) but the terminal frame lost. Reconcile instead
        // of guessing. Never silently reset to Step 1 or pretend failure.
        planning = false;
        stopPlanTimer();
        if (planState !== 'completed') {
          reconcilePlan(operationId);
        }
      },
      () => {
        // Transport error: reconcile to find out what actually happened.
        planning = false;
        stopPlanTimer();
        if (planState !== 'completed') {
          planState = 'unknown';
          toast.error('Connection error during planning — checking status…');
          reconcilePlan(operationId);
        }
      }
    );
  }

  function getServerName(id: number): string {
    const s = servers.find(s => s.id === id);
    return s ? `${s.name} (${s.host})` : '';
  }
</script>

<div class="page-gutter max-w-3xl">
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

          <!-- Phase 5E (E): execution location drives where the dump/restore
               actually runs. Host = localhost on the server; Container = docker
               exec into a named container; Compose = a service in a compose file. -->
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div>
              <label for="dbExecMode" class="text-xs font-medium text-fg block mb-1">Execution location</label>
              <select id="dbExecMode" bind:value={dbExecMode} class="w-full p-2 border border-border rounded text-sm bg-surface">
                <option value="host">Host (localhost on server)</option>
                <option value="container">Docker container</option>
                <option value="compose">Docker compose service</option>
              </select>
            </div>
            {#if dbExecMode === 'container'}
              <div>
                <label for="dbContainer" class="text-xs font-medium text-fg block mb-1">Container name</label>
                <input id="dbContainer" bind:value={dbContainer} placeholder="e.g. postgres-prod" class="w-full p-2 border border-border rounded text-sm font-mono bg-surface" />
              </div>
            {:else if dbExecMode === 'compose'}
              <div>
                <label for="dbComposeService" class="text-xs font-medium text-fg block mb-1">Compose service</label>
                <input id="dbComposeService" bind:value={dbComposeService} placeholder="e.g. db" class="w-full p-2 border border-border rounded text-sm font-mono bg-surface" />
              </div>
              <div class="sm:col-span-2">
                <label for="dbComposeFile" class="text-xs font-medium text-fg block mb-1">Compose file (optional)</label>
                <input id="dbComposeFile" bind:value={dbComposeFile} placeholder="empty = auto; else /path/docker-compose.yml" class="w-full p-2 border border-border rounded text-sm font-mono bg-surface" />
              </div>
            {/if}
          </div>
          {#if dbExecMode === 'container'}
            <p class="text-xs text-fg-subtle">Runs inside <span class="font-mono">{dbContainer}</span> via <span class="font-mono">docker exec</span>; host/port are ignored (targets the container's loopback).</p>
          {:else if dbExecMode === 'compose'}
            <p class="text-xs text-fg-subtle">Runs the <span class="font-mono">{dbComposeService}</span> service via <span class="font-mono">docker compose exec</span>.</p>
          {/if}
          {#if dbEngine === 'redis'}
            <p class="text-xs text-fg-subtle">Redis uses an RDB snapshot (no per-DB name/username); name/username are ignored.</p>
          {/if}

          <!-- Phase 5E (E): per-engine capability is disclosed honestly — what
               the transfer can resume, the real downtime class, and the warnings. -->
          <div class="mt-4 pt-4 border-t border-border space-y-3">
            <div class="flex flex-wrap items-center gap-2">
              <span class="text-sm font-medium text-fg">Migration mode</span>
              <span
                class="px-2 py-0.5 rounded text-xs border {dbMigrationMode === 'snapshot_copy'
                  ? 'bg-success/10 text-success border-success/30'
                  : 'bg-surface-muted text-fg-muted border-border'}"
              >
                {dbMigrationMode === 'snapshot_copy' ? 'Snapshot copy (available)' : 'Live replication (not available)'}
              </span>
            </div>
            <div>
              <label for="dbMigrationMode" class="text-xs font-medium text-fg block mb-1">Mode</label>
              <select id="dbMigrationMode" bind:value={dbMigrationMode} class="w-full p-2 border border-border rounded text-sm bg-surface">
                <option value="snapshot_copy">Snapshot copy — offline dump &amp; restore (downtime = transfer time)</option>
                <option value="live_replication" disabled>Live replication — not yet wired (would need seeded replica + fenced cutover)</option>
              </select>
            </div>

            <div class="rounded border border-border bg-surface-subtle p-3 space-y-2">
              <p class="text-xs font-semibold text-fg">What this engine will actually do</p>
              <div class="flex flex-wrap gap-x-4 gap-y-1 text-xs text-fg-subtle">
                <span>Resume after interruption:
                  <span class="font-medium text-fg">{dbEngineResume(dbEngine)}</span>
                </span>
                <span>Downtime:
                  <span class="font-medium text-fg">{dbEngineDowntime(dbEngine)}</span>
                </span>
              </div>
              <p class="text-xs text-fg-subtle">{dbEngineTransferNote(dbEngine)}</p>
            </div>

            <div class="flex flex-wrap items-center gap-2">
              <span class="text-sm font-medium text-fg">Engine support</span>
              <SupportStatusBadge level={engineSupport(dbEngine, policy).level} label={engineSupport(dbEngine, policy).label} />
            </div>
            {#if policyError}
              <p class="text-xs text-warning">Support policy unavailable ({policyError}); automatic cutover is disabled. You may still plan a manual migration.</p>
            {/if}

            <div>
              <label for="trafficProvider" class="text-xs font-medium text-fg block mb-1">Traffic provider (cutover)</label>
              <select
                id="trafficProvider"
                bind:value={trafficProvider}
                class="w-full p-2 border border-border rounded text-sm bg-surface"
              >
                <option value="">Manual only (operator performs cutover)</option>
                {#each policy?.supportedTrafficProviders ?? [] as p}
                  <option value={p}>{p} (automatic, fenced)</option>
                {/each}
                {#each (policy?.supportedTrafficProviders ?? []).length ? ['traefik', 'cloudflare', 'docker', 'dns'] : [] as p}
                  <option value={p} disabled>{p} (manual only — no fenced switcher)</option>
                {/each}
              </select>
            </div>

            <div class="flex flex-wrap items-center gap-2">
              <span class="text-sm font-medium text-fg">Provider support</span>
              <SupportStatusBadge level={trafficProviderSupport(trafficProvider, policy).level} label={trafficProviderSupport(trafficProvider, policy).label} />
            </div>

            <label class="flex items-start gap-2 cursor-pointer">
              <input
                type="checkbox"
                bind:checked={autoCutover}
                disabled={!autoCutoverAllowed}
                class="mt-1"
              />
              <span class="text-sm text-fg">
                Automatic cutover (fenced)
                {#if !autoCutoverAllowed}
                  <span class="block text-xs text-fg-subtle">{canAutoCutover(dbEngine, trafficProvider, policy).reason}</span>
                {/if}
              </span>
            </label>
          </div>
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
        <div class="bg-surface-muted border border-border rounded-lg p-3 text-xs text-fg-subtle">
          Detailed comparison and selection happen in the pipeline after the plan is created —
          the <strong>Compare &amp; select items</strong> step lets you review per-item source vs
          target differences and choose what to apply before running the initial sync.
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
              {#if dbExecMode === 'container'} · container: {dbContainer}{:else if dbExecMode === 'compose'} · compose: {dbComposeService}{/if}
              {#if dbMigrationMode === 'snapshot_copy'} · snapshot copy{:else} · {dbMigrationMode}{/if}
            </p>
            <p class="text-xs text-fg-subtle mt-1">
              {#if dbEstimatedBytes > 0}Estimated size: <span class="font-mono text-fg">{formatBytes(dbEstimatedBytes)}</span>{:else}Estimated size: <span class="font-mono text-fg-muted">unknown until plan collects</span>{/if}
              · Resume: <span class="font-mono text-fg">{dbEngineResume(dbEngine)}</span>
              · {dbEngineDowntime(dbEngine)}
            </p>
          </div>
        {/if}
        <div>
          <p class="text-sm text-fg-subtle">Cutover</p>
          <p class="font-mono text-xs text-fg-muted">
            {trafficProvider
              ? `${trafficProvider}${autoCutoverAllowed && autoCutover ? ' (automatic, fenced)' : ' (manual)'}`
              : 'Manual — operator performs cutover'}
          </p>
        </div>
      </div>

      {#if configureError}
        <div class="bg-warning/10 border border-warning/30 text-warning rounded-lg p-3 text-sm">
          Cutover configuration was not applied: {configureError}
        </div>
      {/if}

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

      <!-- Truthful, non-hanging status banner. The wizard never shows a bare
           spinner with no reconcile path; every in-flight state has an honest
           label, and ambiguous outcomes are shown as UNKNOWN, not fake success. -->
      {#if planState === 'connecting'}
        <div class="flex items-center justify-center gap-2 text-sm text-fg-subtle" role="status" aria-live="polite">
          <Loader size={16} class="animate-spin" />
          Connecting to planner…
        </div>
      {:else if planState === 'collecting'}
        <div class="flex items-center justify-center gap-2 text-sm text-fg-subtle" role="status" aria-live="polite">
          <Loader size={16} class="animate-spin" />
          Planning… {Math.floor(planElapsed / 60)}:{String(planElapsed % 60).padStart(2, '0')}
        </div>
      {:else if planState === 'checking'}
        <div class="flex items-center justify-center gap-2 text-sm text-fg-subtle" role="status" aria-live="polite">
          <Loader size={16} class="animate-spin" />
          Checking plan status… (reconnecting to server)
        </div>
      {:else if planState === 'unknown'}
        <div class="bg-warning/10 border border-warning/30 text-warning rounded-lg p-3 text-sm" role="status" aria-live="polite">
          Plan status could not be confirmed. Your plan may already exist —
          check <a class="underline" href="/migrations">Migration History</a>.
          If no plan appears there, you can safely retry.
        </div>
      {:else if planState === 'failed'}
        <div class="bg-error/10 border border-error/30 text-error rounded-lg p-3 text-sm" role="alert">
          Migration planning failed{recoveredStatus ? ` (status: ${recoveredStatus})` : ''}.
          You can retry — a fresh plan will be created.
        </div>
      {/if}

      <!-- Action button. Disabled while in-flight; never abandons a running
           operation. 'unknown' offers a safe Retry (reuses the same op id) AND a
           link to history so the user can verify before retrying. -->
      {#if planState === 'completed'}
        <button
          type="button"
          disabled
          class="w-full px-4 py-3 bg-success/20 text-success rounded-lg font-medium cursor-default"
        >
          Plan created — redirecting…
        </button>
      {:else if planState === 'failed'}
        <button
          type="button"
          on:click={startPlanning}
          class="btn btn-primary btn-md w-full"
        >
          Retry Migration Plan
        </button>
      {:else if planState === 'unknown'}
        <div class="space-y-2">
          <button
            type="button"
            on:click={startPlanning}
            class="btn btn-primary btn-md w-full"
          >
            Retry Migration Plan
          </button>
          <a
            href="/migrations"
            class="btn btn-secondary btn-md w-full"
          >
            Go to Migration History
          </a>
        </div>
      {:else if planInFlight}
        <div class="flex items-center justify-center gap-2 text-sm text-fg-subtle opacity-60">
          <Loader size={16} class="animate-spin" />
          {planState === 'checking' ? 'Reconciling…' : 'Working…'}
        </div>
      {:else}
        <button
          type="button"
          on:click={startPlanning}
          class="btn btn-primary btn-md w-full"
        >
          Create Migration Plan
        </button>
      {/if}
    </div>
  {/if}

  <!-- Navigation buttons -->
  {#if step < 4 && !planning}
    <div class="flex justify-between mt-8">
      <button
        on:click={prevStep}
        disabled={step === 1}
        class="btn btn-secondary btn-sm"
      >
        <ArrowLeft size={16} /> Back
      </button>
      <button
        on:click={nextStep}
        disabled={!canProceed}
        class="btn btn-primary btn-sm disabled:opacity-50"
      >
        Next <ArrowRight size={16} />
      </button>
    </div>
  {/if}
</div>
