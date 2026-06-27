# Part 5: Frontend (Sidebar, Wizard, Progress, History)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the frontend for the migration engine — a sidebar navigation, a 4-step migration wizard (select source → select target → choose categories → review & plan), a live progress page (WebSocket), and a migration history page.

**Architecture:** The frontend adds a new "Migrations" section to the existing SvelteKit app. It reuses the existing `api` client and `wsConnect` WebSocket helper. The wizard collects user input, sends a plan request via WebSocket, then redirects to the progress page which streams execution via WebSocket.

**Tech Stack:** SvelteKit, TypeScript, TailwindCSS, lucide-svelte

## Global Constraints

- All new routes live under `web/src/routes/migrations/`
- Reuse existing `api` client from `$lib/api/client`
- Reuse existing `wsConnect` pattern from `$lib/api/websocket`
- Follow the same styling patterns as the existing server pages
- Use lucide-svelte icons consistently
- All API calls go through the Vite proxy (`/api/...`)

---

## File Structure

| File | Responsibility |
|---|---|
| `web/src/lib/api/migrations.ts` | Migration API client functions |
| `web/src/lib/stores/migrations.ts` | Migration Svelte store |
| `web/src/lib/components/Sidebar.svelte` | Left sidebar navigation |
| `web/src/routes/migrations/+page.svelte` | Migration history list page |
| `web/src/routes/migrations/new/+page.svelte` | Migration wizard (4 steps) |
| `web/src/routes/migrations/[id]/+page.svelte` | Migration detail + progress page |
| `web/src/routes/+layout.svelte` | Root layout with sidebar (modify) |

---

### Task 1: Migration API Client

**Files:**
- Create: `web/src/lib/api/migrations.ts`

**Interfaces:**
- Consumes: `api` from `$lib/api/client`
- Produces: Migration TypeScript types, API functions

- [ ] **Step 1: Write migration API client**

```typescript
// web/src/lib/api/migrations.ts
import { api } from '$lib/api/client';

export interface MigrationPlan {
  id: number;
  sourceServerId: number;
  targetServerId: number;
  status: string;
  categories: string[];
  errorMessage: string;
  createdAt: string;
  completedAt: string;
  rolledBackAt: string;
}

export interface MigrationStep {
  id: number;
  migrationId: number;
  category: string;
  action: string;
  status: string;
  data: string;
  error: string;
  createdAt: string;
  completedAt: string;
}

export interface PlanRequest {
  sourceServerId: number;
  targetServerId: number;
  categories: string[];
  configPaths?: string[];
}

export interface WSMessage {
  step: string;
  status: string;
  value?: string;
  error?: string;
}

export const migrationApi = {
  list: () => api.get<MigrationPlan[]>('/migrations'),
  get: (id: number) => api.get<MigrationPlan>(`/migrations/${id}`),
  delete: (id: number) => api.delete(`/migrations/${id}`),
  getSteps: (id: number) => api.get<MigrationStep[]>(`/migrations/${id}/steps`),
  rollback: (id: number) => api.post(`/migrations/${id}/rollback`, {}),
};

export function wsPlan(req: PlanRequest, onMessage: (msg: WSMessage) => void, onClose?: () => void, onError?: () => void): WebSocket {
  const ws = new WebSocket(`ws://${window.location.host}/ws/plan`);
  ws.onopen = () => {
    ws.send(JSON.stringify(req));
  };
  ws.onmessage = (event) => {
    const msg = JSON.parse(event.data) as WSMessage;
    onMessage(msg);
  };
  ws.onclose = () => onClose?.();
  ws.onerror = () => onError?.();
  return ws;
}

export function wsExecute(migrationId: number, onMessage: (msg: WSMessage) => void, onClose?: () => void, onError?: () => void): WebSocket {
  const ws = new WebSocket(`ws://${window.location.host}/ws/migrate/${migrationId}`);
  ws.onmessage = (event) => {
    const msg = JSON.parse(event.data) as WSMessage;
    onMessage(msg);
  };
  ws.onclose = () => onClose?.();
  ws.onerror = () => onError?.();
  return ws;
}

export function wsRollback(migrationId: number, onMessage: (msg: WSMessage) => void, onClose?: () => void, onError?: () => void): WebSocket {
  const ws = new WebSocket(`ws://${window.location.host}/ws/migrate/${migrationId}/rollback`);
  ws.onmessage = (event) => {
    const msg = JSON.parse(event.data) as WSMessage;
    onMessage(msg);
  };
  ws.onclose = () => onClose?.();
  ws.onerror = () => onError?.();
  return ws;
}
```

- [ ] **Step 2: Commit**

```bash
git add web/src/lib/api/migrations.ts
git commit -m "feat: add migration API client with WebSocket helpers"
```

---

### Task 2: Migration Store

**Files:**
- Create: `web/src/lib/stores/migrations.ts`

**Interfaces:**
- Consumes: `migrationApi` from `$lib/api/migrations`
- Produces: Svelte stores for migration state

- [ ] **Step 1: Write migration store**

```typescript
// web/src/lib/stores/migrations.ts
import { writable, derived } from 'svelte/store';
import { migrationApi, type MigrationPlan, type MigrationStep } from '$lib/api/migrations';

export const migrations = writable<MigrationPlan[]>([]);
export const currentMigration = writable<MigrationPlan | null>(null);
export const migrationSteps = writable<MigrationStep[]>([]);
export const loading = writable(false);
export const error = writable<string | null>(null);

export async function loadMigrations() {
  loading.set(true);
  error.set(null);
  try {
    const data = await migrationApi.list();
    migrations.set(data);
  } catch (e: any) {
    error.set(e.message || 'Failed to load migrations');
  } finally {
    loading.set(false);
  }
}

export async function loadMigration(id: number) {
  loading.set(true);
  error.set(null);
  try {
    const [plan, steps] = await Promise.all([
      migrationApi.get(id),
      migrationApi.getSteps(id),
    ]);
    currentMigration.set(plan);
    migrationSteps.set(steps);
  } catch (e: any) {
    error.set(e.message || 'Failed to load migration');
  } finally {
    loading.set(false);
  }
}

export async function deleteMigration(id: number) {
  try {
    await migrationApi.delete(id);
    migrations.update(m => m.filter(mig => mig.id !== id));
  } catch (e: any) {
    error.set(e.message || 'Failed to delete migration');
  }
}

export function resetMigration() {
  currentMigration.set(null);
  migrationSteps.set([]);
  error.set(null);
}
```

- [ ] **Step 2: Commit**

```bash
git add web/src/lib/stores/migrations.ts
git commit -m "feat: add migration Svelte store with load/delete actions"
```

---

### Task 3: Sidebar Component

**Files:**
- Create: `web/src/lib/components/Sidebar.svelte`

**Interfaces:**
- Consumes: `$app/stores` for active route
- Produces: Sidebar with navigation links

- [ ] **Step 1: Write sidebar component**

```svelte
<!-- web/src/lib/components/Sidebar.svelte -->
<script lang="ts">
  import { page } from '$app/stores';
  import { Server, ArrowRightLeft, History, Settings } from 'lucide-svelte';

  const navItems = [
    { href: '/servers', label: 'Servers', icon: Server },
    { href: '/migrations', label: 'Migrations', icon: ArrowRightLeft },
    { href: '/migrations/history', label: 'History', icon: History },
  ];
</script>

<aside class="w-60 bg-white border-r h-screen flex flex-col">
  <div class="p-4 border-b">
    <h1 class="text-lg font-bold flex items-center gap-2">
      <span class="w-8 h-8 bg-blue-600 rounded-lg flex items-center justify-center text-white text-sm font-bold">M</span>
      Meshium
    </h1>
  </div>

  <nav class="flex-1 p-2">
    {#each navItems as item}
      <a
        href={item.href}
        class="flex items-center gap-3 px-3 py-2 rounded-lg text-sm transition-colors
          {$page.url.pathname.startsWith(item.href)
            ? 'bg-blue-50 text-blue-700 font-medium'
            : 'text-gray-600 hover:bg-gray-50'}"
      >
        <item.icon size={18} />
        {item.label}
      </a>
    {/each}
  </nav>

  <div class="p-2 border-t">
    <a href="/settings" class="flex items-center gap-3 px-3 py-2 rounded-lg text-sm text-gray-600 hover:bg-gray-50">
      <Settings size={18} />
      Settings
    </a>
  </div>
</aside>
```

- [ ] **Step 2: Commit**

```bash
git add web/src/lib/components/Sidebar.svelte
git commit -m "feat: add sidebar navigation component"
```

---

### Task 4: Root Layout with Sidebar

**Files:**
- Create: `web/src/routes/+layout.svelte`

**Interfaces:**
- Consumes: `Sidebar` component

- [ ] **Step 1: Write root layout**

```svelte
<!-- web/src/routes/+layout.svelte -->
<script lang="ts">
  import Sidebar from '$lib/components/Sidebar.svelte';
  import '../app.css';
</script>

<div class="flex h-screen">
  <Sidebar />
  <main class="flex-1 overflow-auto bg-gray-50">
    <slot />
  </main>
</div>
```

- [ ] **Step 2: Update existing server list page to work with the new layout**

The existing `web/src/routes/servers/+page.svelte` may have its own full-page wrapper. Update it to remove the `min-h-screen` wrapper and let the layout handle it. Change:

```svelte
<!-- Remove: <div class="min-h-screen bg-gray-50"> -->
<!-- Replace with: <div> -->
```

- [ ] **Step 3: Commit**

```bash
git add web/src/routes/+layout.svelte
git commit -m "feat: add root layout with sidebar navigation"
```

---

### Task 5: Migration Wizard Page (4-Step)

**Files:**
- Create: `web/src/routes/migrations/new/+page.svelte`

**Interfaces:**
- Consumes: `migrationApi`, `wsPlan`, server list API
- Produces: 4-step wizard for creating a migration plan

- [ ] **Step 1: Write the migration wizard**

```svelte
<!-- web/src/routes/migrations/new/+page.svelte -->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { api } from '$lib/api/client';
  import { wsPlan, type WSMessage, type PlanRequest } from '$lib/api/migrations';
  import type { Server } from '$lib/stores/servers';
  import { ArrowLeft, ArrowRight, Check, Package, FileCode, Settings, Users, Loader } from 'lucide-svelte';

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
  ];

  onMount(async () => {
    try {
      servers = await api.get<Server[]>('/servers');
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
          // Redirect to migrations list after a short delay
          setTimeout(() => goto('/migrations'), 1000);
        }
      },
      () => { planning = false; },
      () => { planning = false; }
    );
  }

  function getServerName(id: number): string {
    const s = servers.find(s => s.id === id);
    return s ? `${s.name} (${s.host})` : '';
  }
</script>

<div class="p-6 max-w-3xl mx-auto">
  <div class="flex items-center gap-2 mb-6">
    <a href="/migrations" class="text-sm text-gray-600 hover:text-gray-900 flex items-center gap-1">
      <ArrowLeft size={16} /> Back to Migrations
    </a>
  </div>

  <h1 class="text-2xl font-bold mb-2">New Migration</h1>
  <p class="text-sm text-gray-500 mb-6">Step {step} of 4</p>

  <!-- Progress bar -->
  <div class="flex items-center mb-8">
    {#each [1, 2, 3, 4] as s}
      <div class="flex items-center {s < 4 ? 'flex-1' : ''}">
        <div class="w-8 h-8 rounded-full flex items-center justify-center text-sm font-medium
          {s < step ? 'bg-green-500 text-white' : s === step ? 'bg-blue-600 text-white' : 'bg-gray-200 text-gray-500'}">
          {#if s < step}
            <Check size={16} />
          {:else}
            {s}
          {/if}
        </div>
        {#if s < 4}
          <div class="h-0.5 flex-1 mx-2 {s < step ? 'bg-green-500' : 'bg-gray-200'}"></div>
        {/if}
      </div>
    {/each}
  </div>

  <!-- Step 1: Select Source -->
  {#if step === 1}
    <div class="space-y-4">
      <h2 class="text-lg font-semibold">Select Source Server</h2>
      <p class="text-sm text-gray-500">Choose the server to migrate FROM.</p>
      <div class="space-y-2">
        {#each servers as s}
          <button
            on:click={() => sourceServerId = s.id}
            class="w-full text-left p-4 rounded-lg border transition-colors
              {sourceServerId === s.id ? 'border-blue-500 bg-blue-50' : 'border-gray-200 hover:border-gray-300'}"
          >
            <div class="flex items-center justify-between">
              <div>
                <p class="font-medium">{s.name}</p>
                <p class="text-sm text-gray-500">{s.host}:{s.port} · {s.username}</p>
              </div>
              {#if sourceServerId === s.id}
                <Check class="text-blue-600" size={20} />
              {/if}
            </div>
          </button>
        {/each}
      </div>
    </div>

  <!-- Step 2: Select Target -->
  {:else if step === 2}
    <div class="space-y-4">
      <h2 class="text-lg font-semibold">Select Target Server</h2>
      <p class="text-sm text-gray-500">Choose the server to migrate TO. Must be different from source.</p>
      <div class="space-y-2">
        {#each servers.filter(s => s.id !== sourceServerId) as s}
          <button
            on:click={() => targetServerId = s.id}
            class="w-full text-left p-4 rounded-lg border transition-colors
              {targetServerId === s.id ? 'border-blue-500 bg-blue-50' : 'border-gray-200 hover:border-gray-300'}"
          >
            <div class="flex items-center justify-between">
              <div>
                <p class="font-medium">{s.name}</p>
                <p class="text-sm text-gray-500">{s.host}:{s.port} · {s.username}</p>
              </div>
              {#if targetServerId === s.id}
                <Check class="text-blue-600" size={20} />
              {/if}
            </div>
          </button>
        {/each}
      </div>
    </div>

  <!-- Step 3: Choose Categories -->
  {:else if step === 3}
    <div class="space-y-4">
      <h2 class="text-lg font-semibold">Choose Categories to Migrate</h2>
      <p class="text-sm text-gray-500">Select what to migrate from source to target.</p>
      <div class="grid grid-cols-2 gap-3">
        {#each categories as cat}
          <button
            on:click={() => toggleCategory(cat.id)}
            class="p-4 rounded-lg border text-left transition-colors
              {selectedCategories.includes(cat.id) ? 'border-blue-500 bg-blue-50' : 'border-gray-200 hover:border-gray-300'}"
          >
            <div class="flex items-start gap-3">
              <cat.icon size={20} class="text-gray-600 mt-0.5" />
              <div>
                <p class="font-medium">{cat.label}</p>
                <p class="text-xs text-gray-500 mt-1">{cat.desc}</p>
              </div>
            </div>
          </button>
        {/each}
      </div>

      {#if selectedCategories.includes('configs')}
        <div class="mt-4">
          <label class="text-sm font-medium block mb-1">Config Paths (optional)</label>
          <p class="text-xs text-gray-500 mb-2">One path per line. Default: /etc/</p>
          <textarea
            bind:value={configPaths}
            placeholder="/etc/nginx/&#10;/etc/systemd/system/"
            class="w-full p-2 border rounded text-sm font-mono"
            rows="3"
          ></textarea>
        </div>
      {/if}
    </div>

  <!-- Step 4: Review & Plan -->
  {:else if step === 4}
    <div class="space-y-4">
      <h2 class="text-lg font-semibold">Review & Create Plan</h2>
      <div class="bg-white rounded-lg border p-4 space-y-3">
        <div>
          <p class="text-sm text-gray-500">Source</p>
          <p class="font-medium">{getServerName(sourceServerId)}</p>
        </div>
        <div>
          <p class="text-sm text-gray-500">Target</p>
          <p class="font-medium">{getServerName(targetServerId)}</p>
        </div>
        <div>
          <p class="text-sm text-gray-500">Categories</p>
          <div class="flex flex-wrap gap-2 mt-1">
            {#each selectedCategories as cat}
              <span class="px-2 py-1 bg-blue-50 text-blue-700 text-xs rounded">{cat}</span>
            {/each}
          </div>
        </div>
        {#if configPaths}
          <div>
            <p class="text-sm text-gray-500">Config Paths</p>
            <p class="font-mono text-xs">{configPaths}</p>
          </div>
        {/if}
      </div>

      {#if planMessages.length > 0}
        <div class="bg-gray-900 text-gray-100 rounded-lg p-4 max-h-60 overflow-auto">
          <div class="space-y-1">
            {#each planMessages as msg}
              <div class="text-xs font-mono">
                <span class={msg.status === 'error' ? 'text-red-400' : msg.status === 'success' || msg.status === 'complete' ? 'text-green-400' : 'text-gray-400'}>
                  [{msg.status}]
                </span>
                <span class="text-gray-300">{msg.step}</span>
                {#if msg.value}
                  <span class="text-gray-500">→ {msg.value}</span>
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
        <div class="flex items-center justify-center gap-2 text-sm text-gray-500">
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
        class="flex items-center gap-1 px-4 py-2 text-sm text-gray-600 hover:text-gray-900 disabled:opacity-50"
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
```

- [ ] **Step 2: Commit**

```bash
git add web/src/routes/migrations/new/+page.svelte
git commit -m "feat: add 4-step migration wizard page"
```

---

### Task 6: Migration Progress Page (WebSocket)

**Files:**
- Create: `web/src/routes/migrations/[id]/+page.svelte`

**Interfaces:**
- Consumes: `migrationApi`, `wsExecute`, `wsRollback`
- Produces: Migration detail page with live progress and rollback

- [ ] **Step 1: Write the migration progress page**

```svelte
<!-- web/src/routes/migrations/[id]/+page.svelte -->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { page } from '$app/stores';
  import { migrationApi, wsExecute, wsRollback, type WSMessage, type MigrationPlan, type MigrationStep } from '$lib/api/migrations';
  import { ArrowLeft, Play, Undo2, Trash2, CheckCircle, XCircle, Loader, Clock } from 'lucide-svelte';

  const migrationId = parseInt($page.params.id);
  let plan: MigrationPlan | null = null;
  let steps: MigrationStep[] = [];
  let loading = true;
  let executing = false;
  let rollingBack = false;
  let progressMessages: WSMessage[] = [];
  let ws: WebSocket | null = null;

  onMount(async () => {
    try {
      plan = await migrationApi.get(migrationId);
      steps = await migrationApi.getSteps(migrationId);
    } catch {
      // handle error
    } finally {
      loading = false;
    }
  });

  onDestroy(() => {
    ws?.close();
  });

  function startExecution() {
    executing = true;
    progressMessages = [];

    ws = wsExecute(
      migrationId,
      (msg: WSMessage) => {
        progressMessages = [...progressMessages, msg];
        if (msg.step === 'execute' && (msg.status === 'complete' || msg.status === 'error')) {
          executing = false;
          // Refresh plan
          refreshPlan();
        }
      },
      () => { executing = false; },
      () => { executing = false; }
    );
  }

  function startRollback() {
    rollingBack = true;
    progressMessages = [];

    ws = wsRollback(
      migrationId,
      (msg: WSMessage) => {
        progressMessages = [...progressMessages, msg];
        if (msg.step === 'rollback' && (msg.status === 'complete' || msg.status === 'error')) {
          rollingBack = false;
          refreshPlan();
        }
      },
      () => { rollingBack = false; },
      () => { rollingBack = false; }
    );
  }

  async function refreshPlan() {
    try {
      plan = await migrationApi.get(migrationId);
      steps = await migrationApi.getSteps(migrationId);
    } catch {
      // ignore
    }
  }

  async function deleteMigration() {
    if (!confirm('Delete this migration? This cannot be undone.')) return;
    await migrationApi.delete(migrationId);
    window.location.href = '/migrations';
  }

  function statusColor(status: string): string {
    switch (status) {
      case 'completed': case 'success': return 'text-green-600';
      case 'failed': case 'error': return 'text-red-600';
      case 'running': case 'progress': return 'text-blue-600';
      case 'planned': return 'text-gray-500';
      case 'rolled_back': return 'text-yellow-600';
      default: return 'text-gray-500';
    }
  }

  function statusIcon(status: string) {
    switch (status) {
      case 'completed': case 'success': return CheckCircle;
      case 'failed': case 'error': return XCircle;
      case 'running': case 'progress': return Loader;
      default: return Clock;
    }
  }
</script>

<div class="p-6">
  <a href="/migrations" class="text-sm text-gray-600 hover:text-gray-900 flex items-center gap-1 mb-4">
    <ArrowLeft size={16} /> Back to Migrations
  </a>

  {#if loading}
    <p class="text-gray-500">Loading...</p>
  {:else if !plan}
    <p class="text-red-500">Migration not found</p>
  {:else}
    <div class="flex items-center justify-between mb-6">
      <div>
        <h1 class="text-xl font-bold">Migration #{plan.id}</h1>
        <p class="text-sm text-gray-500">
          Source: Server #{plan.sourceServerId} → Target: Server #{plan.targetServerId}
        </p>
      </div>
      <div class="flex items-center gap-2">
        {#if plan.status === 'planned'}
          <button
            on:click={startExecution}
            disabled={executing}
            class="flex items-center gap-1 px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50"
          >
            <Play size={16} /> {executing ? 'Executing...' : 'Execute'}
          </button>
        {/if}
        {#if plan.status === 'completed' || plan.status === 'failed'}
          <button
            on:click={startRollback}
            disabled={rollingBack}
            class="flex items-center gap-1 px-4 py-2 bg-yellow-600 text-white rounded hover:bg-yellow-700 disabled:opacity-50"
          >
            <Undo2 size={16} /> {rollingBack ? 'Rolling back...' : 'Rollback'}
          </button>
        {/if}
        <button
          on:click={deleteMigration}
          class="flex items-center gap-1 px-4 py-2 text-red-600 border border-red-200 rounded hover:bg-red-50"
        >
          <Trash2 size={16} /> Delete
        </button>
      </div>
    </div>

    <!-- Status badge -->
    <div class="mb-4">
      <span class="px-3 py-1 rounded-full text-sm font-medium
        {plan.status === 'completed' ? 'bg-green-100 text-green-700' :
         plan.status === 'failed' ? 'bg-red-100 text-red-700' :
         plan.status === 'running' ? 'bg-blue-100 text-blue-700' :
         plan.status === 'rolled_back' ? 'bg-yellow-100 text-yellow-700' :
         'bg-gray-100 text-gray-700'}">
        {plan.status}
      </span>
    </div>

    <!-- Categories -->
    <div class="mb-6">
      <h2 class="text-sm font-semibold mb-2">Categories</h2>
      <div class="flex flex-wrap gap-2">
        {#each plan.categories as cat}
          <span class="px-2 py-1 bg-blue-50 text-blue-700 text-xs rounded">{cat}</span>
        {/each}
      </div>
    </div>

    <!-- Steps -->
    {#if steps.length > 0}
      <div class="mb-6">
        <h2 class="text-sm font-semibold mb-2">Steps</h2>
        <div class="space-y-2">
          {#each steps as step}
            <div class="flex items-center gap-2 text-sm">
              <span class="w-2 h-2 rounded-full
                {step.status === 'completed' ? 'bg-green-500' :
                 step.status === 'failed' ? 'bg-red-500' :
                 step.status === 'running' ? 'bg-blue-500' :
                 'bg-gray-300'}">
              </span>
              <span class="font-mono">{step.category}:{step.action}</span>
              <span class={statusColor(step.status)}>{step.status}</span>
              {#if step.error}
                <span class="text-red-500">→ {step.error}</span>
              {/if}
            </div>
          {/each}
        </div>
      </div>
    {/if}

    <!-- Live progress -->
    {#if progressMessages.length > 0}
      <div class="bg-gray-900 text-gray-100 rounded-lg p-4 max-h-96 overflow-auto">
        <h3 class="text-xs font-semibold mb-2 text-gray-400">Live Progress</h3>
        <div class="space-y-1">
          {#each progressMessages as msg}
            <div class="text-xs font-mono">
              <span class={msg.status === 'error' ? 'text-red-400' : msg.status === 'success' || msg.status === 'complete' ? 'text-green-400' : 'text-blue-400'}>
                [{msg.status}]
              </span>
              <span class="text-gray-300">{msg.step}</span>
              {#if msg.value}
                <span class="text-gray-500">→ {msg.value}</span>
              {/if}
              {#if msg.error}
                <span class="text-red-400">→ {msg.error}</span>
              {/if}
            </div>
          {/each}
        </div>
      </div>
    {/if}
  {/if}
</div>
```

- [ ] **Step 2: Commit**

```bash
git add web/src/routes/migrations/[id]/+page.svelte
git commit -m "feat: add migration progress page with WebSocket live updates"
```

---

### Task 7: Migration History Page

**Files:**
- Create: `web/src/routes/migrations/+page.svelte`

**Interfaces:**
- Consumes: `migrationApi`, `migrations` store
- Produces: Migration history list with status badges

- [ ] **Step 1: Write the migration history page**

```svelte
<!-- web/src/routes/migrations/+page.svelte -->
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
      case 'planned': return 'bg-gray-100 text-gray-700';
      case 'rolled_back': return 'bg-yellow-100 text-yellow-700';
      default: return 'bg-gray-100 text-gray-700';
    }
  }

  function statusIcon(status: string) {
    switch (status) {
      case 'completed': return CheckCircle;
      case 'failed': return XCircle;
      case 'running': return Loader;
      default: return Clock;
    }
  }
</script>

<div class="p-6 max-w-4xl mx-auto">
  <div class="flex items-center justify-between mb-6">
    <h1 class="text-xl font-bold">Migrations</h1>
    <a
      href="/migrations/new"
      class="flex items-center gap-1 px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 text-sm"
    >
      <Plus size={16} /> New Migration
    </a>
  </div>

  {#if loading}
    <p class="text-gray-500">Loading...</p>
  {:else if migrations.length === 0}
    <div class="text-center py-12">
      <p class="text-gray-500 mb-4">No migrations yet</p>
      <a href="/migrations/new" class="text-blue-600 hover:underline">Create your first migration →</a>
    </div>
  {:else}
    <div class="space-y-2">
      {#each migrations as m}
        <a
          href="/migrations/{m.id}"
          class="block p-4 bg-white rounded-lg border hover:border-gray-300 transition-colors"
        >
          <div class="flex items-center justify-between">
            <div class="flex items-center gap-3">
              <div class="flex items-center gap-2">
                <span class="text-sm font-medium">Server #{m.sourceServerId}</span>
                <ArrowRight size={14} class="text-gray-400" />
                <span class="text-sm font-medium">Server #{m.targetServerId}</span>
              </div>
            </div>
            <div class="flex items-center gap-3">
              <span class="px-2 py-1 rounded-full text-xs font-medium {statusBadge(m.status)}">
                {m.status}
              </span>
              <span class="text-xs text-gray-500">{m.createdAt}</span>
              <button
                on:click={(e) => deleteMigration(m.id, e)}
                class="text-gray-400 hover:text-red-500"
              >
                <Trash2 size={16} />
              </button>
            </div>
          </div>
          <div class="flex flex-wrap gap-1 mt-2">
            {#each m.categories as cat}
              <span class="px-2 py-0.5 bg-gray-50 text-gray-600 text-xs rounded">{cat}</span>
            {/each}
          </div>
        </a>
      {/each}
    </div>
  {/if}
</div>
```

- [ ] **Step 2: Commit**

```bash
git add web/src/routes/migrations/+page.svelte
git commit -m "feat: add migration history list page"
```

---

### Task 8: Build & Verify

**Files:**
- No new files

- [ ] **Step 1: Verify the frontend builds**

```bash
cd /root/meshium/web
npm run build
```

- [ ] **Step 2: Verify the Go server compiles with the frontend embedded**

```bash
cd /root/meshium
go build ./cmd/server/
```

- [ ] **Step 3: Run the server and verify pages load**

```bash
# Start server in background
go run ./cmd/server/ &
sleep 2

# Test migrations page
curl -s http://localhost:8080/migrations | head -20

# Test migrations API
curl -s http://localhost:8080/api/migrations

# Kill server
kill %1
```

- [ ] **Step 4: Commit any remaining changes**

```bash
git add -A
git commit -m "chore: verify migration frontend builds and serves correctly"
```
