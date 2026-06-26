# Part 5: Frontend (SvelteKit)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the SvelteKit frontend with auth pages (setup, login), server list, server detail with connection test, add/edit server form, and settings page.

**Architecture:** SvelteKit SPA with shadcn/ui (Svelte port) and TailwindCSS. Svelte stores for auth and server state. REST API client for CRUD operations. WebSocket client for connection test streaming. Go backend serves the built frontend via `//go:embed`.

**Tech Stack:** SvelteKit, shadcn/ui-svelte, TailwindCSS, TypeScript, `lucide-svelte` (icons)

## Global Constraints

- Frontend dir: `web/`
- API base URL: `http://localhost:8080` (dev), same-origin (prod)
- WebSocket base URL: `ws://localhost:8080` (dev), same-origin (prod)
- Auth state: `authStore` (setup, locked, unlocked)
- Server state: `serverStore` (list, selected, loading)
- All API responses use: `{"error": "string", "code": "ERROR_CODE"}` on error
- Credential fields never returned in API responses

---

## File Structure

| File | Responsibility |
|---|---|
| `web/package.json` | NPM dependencies |
| `web/svelte.config.js` | SvelteKit config |
| `web/vite.config.ts` | Vite config with API proxy |
| `web/tailwind.config.ts` | TailwindCSS config |
| `web/postcss.config.js` | PostCSS config |
| `web/src/app.html` | HTML template |
| `web/src/app.css` | Global styles + Tailwind directives |
| `web/src/lib/api/client.ts` | REST API client |
| `web/src/lib/api/websocket.ts` | WebSocket client |
| `web/src/lib/stores/auth.ts` | Auth store (setup, locked, unlocked) |
| `web/src/lib/stores/servers.ts` | Server store (list, selected) |
| `web/src/lib/components/ui/` | shadcn/ui components |
| `web/src/routes/+layout.svelte` | Root layout with auth guard |
| `web/src/routes/+page.svelte` | Redirect to /servers or /setup |
| `web/src/routes/setup/+page.svelte` | Master password setup |
| `web/src/routes/login/+page.svelte` | Unlock app |
| `web/src/routes/servers/+page.svelte` | Server list |
| `web/src/routes/servers/[id]/+page.svelte` | Server detail + connection test |
| `web/src/routes/servers/new/+page.svelte` | Add server form |
| `web/src/routes/servers/[id]/edit/+page.svelte` | Edit server form |
| `web/src/routes/settings/+page.svelte` | SSH key management |

---

### Task 1: SvelteKit Project Setup

**Files:**
- Create: `web/` (entire SvelteKit project)

- [ ] **Step 1: Scaffold SvelteKit project**

```bash
cd /root/meshium
npm create svelte@latest web -- --template minimal --types ts
cd web
npm install
```

- [ ] **Step 2: Install dependencies**

```bash
cd /root/meshium/web
npm install -D tailwindcss postcss autoprefixer
npx tailwindcss init -p
npm install -D @types/node
npm install lucide-svelte
npm install clsx tailwind-merge tailwind-variants
```

- [ ] **Step 3: Configure TailwindCSS**

```ts
// web/tailwind.config.ts
import type { Config } from 'tailwindcss'

const config: Config = {
  content: ['./src/**/*.{html,js,svelte,ts}'],
  theme: {
    extend: {},
  },
  plugins: [],
}

export default config
```

```js
// web/postcss.config.js
export default {
  plugins: {
    tailwindcss: {},
    autoprefixer: {},
  },
}
```

```css
/* web/src/app.css */
@tailwind base;
@tailwind components;
@tailwind utilities;
```

- [ ] **Step 4: Configure Vite with API proxy**

```ts
// web/vite.config.ts
import { sveltekit } from '@sveltejs/kit/vite'
import { defineConfig } from 'vite'

export default defineConfig({
  plugins: [sveltekit()],
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      '/ws': {
        target: 'ws://localhost:8080',
        ws: true,
      },
    },
  },
})
```

- [ ] **Step 5: Configure SvelteKit**

```js
// web/svelte.config.js
import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/kit/vite';

const config = {
  preprocess: vitePreprocess(),
  kit: {
    adapter: adapter({
      pages: 'build',
      assets: 'build',
      fallback: 'index.html',
    }),
  },
};

export default config;
```

- [ ] **Step 6: Verify dev server starts**

```bash
cd /root/meshium/web
npm run dev -- --port 5173 &
sleep 3
curl -s http://localhost:5173
kill %1
```
Expected: HTML response

- [ ] **Step 7: Commit**

```bash
cd /root/meshium
git add web/
git commit -m "feat: scaffold SvelteKit project with TailwindCSS"
```

---

### Task 2: API Client

**Files:**
- Create: `web/src/lib/api/client.ts`
- Create: `web/src/lib/api/websocket.ts`

**Interfaces:**
- Produces: `apiClient` object with `get`, `post`, `put`, `delete`, `patch` methods, `wsConnect` function

- [ ] **Step 1: Write REST API client**

```ts
// web/src/lib/api/client.ts
const BASE = '/api';

export class APIError extends Error {
  code: string;
  constructor(message: string, code: string) {
    super(message);
    this.code = code;
  }
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown
): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : {},
    body: body ? JSON.stringify(body) : undefined,
  });

  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: 'Unknown error', code: 'UNKNOWN' }));
    throw new APIError(err.error, err.code);
  }

  return res.json();
}

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body),
  put: <T>(path: string, body?: unknown) => request<T>('PUT', path, body),
  delete: <T>(path: string) => request<T>('DELETE', path),
  patch: <T>(path: string, body?: unknown) => request<T>('PATCH', path, body),
};
```

- [ ] **Step 2: Write WebSocket client**

```ts
// web/src/lib/api/websocket.ts
export function wsConnect(
  serverId: number,
  onMessage: (msg: WSMessage) => void,
  onError?: (err: Event) => void,
  onClose?: () => void
): WebSocket {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const url = `${proto}://${location.host}/ws/connect/${serverId}`;
  const ws = new WebSocket(url);

  ws.onmessage = (e) => {
    const msg = JSON.parse(e.data) as WSMessage;
    onMessage(msg);
  };

  ws.onerror = (e) => onError?.(e);
  ws.onclose = () => onClose?.();

  return ws;
}

export interface WSMessage {
  step: string;
  status: 'success' | 'error' | 'complete';
  value?: unknown;
  error?: string;
  latencyMs?: number;
}
```

- [ ] **Step 3: Verify it compiles**

```bash
cd /root/meshium/web
npx svelte-check --tsconfig ./tsconfig.json
```

- [ ] **Step 4: Commit**

```bash
cd /root/meshium
git add web/src/lib/api/
git commit -m "feat: add API client (REST + WebSocket)"
```

---

### Task 3: Auth Store

**Files:**
- Create: `web/src/lib/stores/auth.ts`

**Interfaces:**
- Produces: `authStore` with `checkStatus`, `setup`, `unlock`, `lock`

- [ ] **Step 1: Write auth store**

```ts
// web/src/lib/stores/auth.ts
import { writable } from 'svelte/store';
import { api } from '$lib/api/client';

export interface AuthState {
  setup: boolean;
  locked: boolean;
  loading: boolean;
}

export const authStore = writable<AuthState>({
  setup: false,
  locked: true,
  loading: true,
});

export async function checkStatus() {
  authStore.update((s) => ({ ...s, loading: true }));
  try {
    const status = await api.get<{ setup: boolean; locked: boolean }>('/auth/status');
    authStore.set({ ...status, loading: false });
  } catch {
    authStore.set({ setup: false, locked: true, loading: false });
  }
}

export async function setup(password: string) {
  await api.post('/auth/setup', { password });
  await checkStatus();
}

export async function unlock(password: string) {
  await api.post('/auth/unlock', { password });
  await checkStatus();
}

export async function lock() {
  await api.post('/auth/lock');
  await checkStatus();
}
```

- [ ] **Step 2: Commit**

```bash
git add web/src/lib/stores/auth.ts
git commit -m "feat: add auth store"
```

---

### Task 4: Server Store

**Files:**
- Create: `web/src/lib/stores/servers.ts`

**Interfaces:**
- Produces: `serverStore` with `fetchServers`, `createServer`, `updateServer`, `deleteServer`, `toggleFavorite`

- [ ] **Step 1: Write server store**

```ts
// web/src/lib/stores/servers.ts
import { writable } from 'svelte/store';
import { api } from '$lib/api/client';

export interface Server {
  id: number;
  name: string;
  description: string;
  host: string;
  port: number;
  username: string;
  tags: string[];
  environment: string;
  region: string;
  icon: string;
  color: string;
  favorite: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface ServerInfo {
  sshStatus: string;
  latencyMs: number;
  hostname: string;
  os: string;
  kernel: string;
  architecture: string;
  cpuModel: string;
  cpuCores: number;
  ramTotalMb: number;
  diskTotalGb: number;
  virtualization: string;
  provider: string;
  publicIp: string;
  privateIp: string;
  timezone: string;
}

export const serverStore = writable<{
  servers: Server[];
  loading: boolean;
  error: string | null;
}>({
  servers: [],
  loading: false,
  error: null,
});

export async function fetchServers(filter?: {
  environment?: string;
  region?: string;
  tag?: string;
  q?: string;
}) {
  serverStore.update((s) => ({ ...s, loading: true, error: null }));

  const params = new URLSearchParams();
  if (filter?.environment) params.set('environment', filter.environment);
  if (filter?.region) params.set('region', filter.region);
  if (filter?.tag) params.set('tag', filter.tag);
  if (filter?.q) params.set('q', filter.q);

  const query = params.toString() ? `?${params}` : '';
  try {
    const servers = await api.get<Server[]>(`/servers${query}`);
    serverStore.set({ servers, loading: false, error: null });
  } catch (e) {
    serverStore.set({ servers: [], loading: false, error: (e as Error).message });
  }
}

export async function createServer(data: Partial<Server> & {
  password?: string;
  sshKey?: string;
  passphrase?: string;
}) {
  const server = await api.post<Server>('/servers', data);
  await fetchServers();
  return server;
}

export async function updateServer(id: number, data: Partial<Server> & {
  password?: string;
  sshKey?: string;
  passphrase?: string;
}) {
  await api.put<Server>(`/servers/${id}`, data);
  await fetchServers();
}

export async function deleteServer(id: number) {
  await api.delete(`/servers/${id}`);
  await fetchServers();
}

export async function toggleFavorite(id: number) {
  await api.patch(`/servers/${id}/favorite`);
  await fetchServers();
}
```

- [ ] **Step 2: Commit**

```bash
git add web/src/lib/stores/servers.ts
git commit -m "feat: add server store"
```

---

### Task 5: Setup Page

**Files:**
- Create: `web/src/routes/setup/+page.svelte`

- [ ] **Step 1: Write setup page**

```svelte
<!-- web/src/routes/setup/+page.svelte -->
<script lang="ts">
  import { setup, authStore } from '$lib/stores/auth';
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';

  let password = '';
  let confirmPassword = '';
  let error = '';
  let loading = false;

  onMount(() => {
    authStore.subscribe((s) => {
      if (s.setup && !s.locked) {
        goto('/servers');
      }
    });
  });

  async function handleSubmit() {
    error = '';
    if (password.length < 8) {
      error = 'Password must be at least 8 characters';
      return;
    }
    if (password !== confirmPassword) {
      error = 'Passwords do not match';
      return;
    }
    loading = true;
    try {
      await setup(password);
      goto('/servers');
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  }
</script>

<div class="min-h-screen flex items-center justify-center bg-gray-50">
  <div class="max-w-md w-full p-8 bg-white rounded-lg shadow">
    <h1 class="text-2xl font-bold mb-2">Welcome to Meshium</h1>
    <p class="text-gray-600 mb-6">Set a master password to encrypt your credentials.</p>

    {#if error}
      <div class="mb-4 p-3 bg-red-50 text-red-700 rounded">{error}</div>
    {/if}

    <form on:submit|preventDefault={handleSubmit} class="space-y-4">
      <div>
        <label for="password" class="block text-sm font-medium mb-1">Master Password</label>
        <input
          id="password"
          type="password"
          bind:value={password}
          class="w-full px-3 py-2 border rounded focus:outline-none focus:ring-2 focus:ring-blue-500"
          placeholder="At least 8 characters"
        />
      </div>
      <div>
        <label for="confirm" class="block text-sm font-medium mb-1">Confirm Password</label>
        <input
          id="confirm"
          type="password"
          bind:value={confirmPassword}
          class="w-full px-3 py-2 border rounded focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
      </div>
      <button
        type="submit"
        disabled={loading}
        class="w-full py-2 bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50"
      >
        {loading ? 'Setting up...' : 'Set Master Password'}
      </button>
    </form>
    <p class="mt-4 text-xs text-gray-500">
      If you forget this password, your credentials cannot be recovered.
    </p>
  </div>
</div>
```

- [ ] **Step 2: Commit**

```bash
git add web/src/routes/setup/+page.svelte
git commit -m "feat: add setup page (master password)"
```

---

### Task 6: Login Page

**Files:**
- Create: `web/src/routes/login/+page.svelte`

- [ ] **Step 1: Write login page**

```svelte
<!-- web/src/routes/login/+page.svelte -->
<script lang="ts">
  import { unlock, authStore } from '$lib/stores/auth';
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';

  let password = '';
  let error = '';
  let loading = false;

  onMount(() => {
    authStore.subscribe((s) => {
      if (!s.setup) {
        goto('/setup');
      }
      if (!s.locked) {
        goto('/servers');
      }
    });
  });

  async function handleSubmit() {
    error = '';
    loading = true;
    try {
      await unlock(password);
      goto('/servers');
    } catch (e) {
      error = 'Invalid password';
    } finally {
      loading = false;
    }
  }
</script>

<div class="min-h-screen flex items-center justify-center bg-gray-50">
  <div class="max-w-md w-full p-8 bg-white rounded-lg shadow">
    <h1 class="text-2xl font-bold mb-6">Unlock Meshium</h1>

    {#if error}
      <div class="mb-4 p-3 bg-red-50 text-red-700 rounded">{error}</div>
    {/if}

    <form on:submit|preventDefault={handleSubmit} class="space-y-4">
      <div>
        <label for="password" class="block text-sm font-medium mb-1">Master Password</label>
        <input
          id="password"
          type="password"
          bind:value={password}
          class="w-full px-3 py-2 border rounded focus:outline-none focus:ring-2 focus:ring-blue-500"
          placeholder="Enter master password"
        />
      </div>
      <button
        type="submit"
        disabled={loading}
        class="w-full py-2 bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50"
      >
        {loading ? 'Unlocking...' : 'Unlock'}
      </button>
    </form>
  </div>
</div>
```

- [ ] **Step 2: Commit**

```bash
git add web/src/routes/login/+page.svelte
git commit -m "feat: add login page (unlock app)"
```

---

### Task 7: Root Layout with Auth Guard

**Files:**
- Create: `web/src/routes/+layout.svelte`
- Create: `web/src/routes/+page.svelte`

- [ ] **Step 1: Write root layout with auth guard**

```svelte
<!-- web/src/routes/+layout.svelte -->
<script lang="ts">
  import '../app.css';
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { authStore, checkStatus } from '$lib/stores/auth';

  onMount(() => {
    checkStatus();
  });

  // Auth guard
  $: {
    const s = $authStore;
    if (s.loading) return;

    const path = $page.url.pathname;
    if (!s.setup && path !== '/setup') {
      goto('/setup');
    } else if (s.setup && s.locked && path !== '/login' && path !== '/setup') {
      goto('/login');
    }
  }
</script>

<slot />
```

- [ ] **Step 2: Write root page (redirect)**

```svelte
<!-- web/src/routes/+page.svelte -->
<script lang="ts">
  import { goto } from '$app/navigation';
  import { authStore } from '$lib/stores/auth';

  $: {
    const s = $authStore;
    if (s.loading) return;
    if (!s.setup) {
      goto('/setup');
    } else if (s.locked) {
      goto('/login');
    } else {
      goto('/servers');
    }
  }
</script>
```

- [ ] **Step 3: Commit**

```bash
git add web/src/routes/+layout.svelte web/src/routes/+page.svelte
git commit -m "feat: add root layout with auth guard"
```

---

### Task 8: Server List Page

**Files:**
- Create: `web/src/routes/servers/+page.svelte`

- [ ] **Step 1: Write server list page**

```svelte
<!-- web/src/routes/servers/+page.svelte -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { serverStore, fetchServers, toggleFavorite } from '$lib/stores/servers';
  import { lock } from '$lib/stores/auth';
  import { Star, Plus, Search, Server } from 'lucide-svelte';

  let searchQuery = '';
  let environmentFilter = '';
  let regionFilter = '';

  onMount(() => {
    fetchServers();
  });

  function handleSearch() {
    fetchServers({
      q: searchQuery || undefined,
      environment: environmentFilter || undefined,
      region: regionFilter || undefined,
    });
  }

  function envColor(env: string): string {
    switch (env) {
      case 'production': return 'bg-red-100 text-red-700';
      case 'staging': return 'bg-yellow-100 text-yellow-700';
      case 'development': return 'bg-green-100 text-green-700';
      default: return 'bg-gray-100 text-gray-700';
    }
  }
</script>

<div class="min-h-screen bg-gray-50">
  <header class="bg-white border-b px-6 py-4 flex items-center justify-between">
    <h1 class="text-xl font-bold">Meshium</h1>
    <button on:click={() => lock()} class="text-sm text-gray-600 hover:text-gray-900">
      Lock
    </button>
  </header>

  <div class="max-w-6xl mx-auto p-6">
    <div class="flex items-center justify-between mb-6">
      <h2 class="text-lg font-semibold">Servers</h2>
      <a href="/servers/new" class="flex items-center gap-1 px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700">
        <Plus size={18} /> Add Server
      </a>
    </div>

    <!-- Filters -->
    <div class="flex gap-4 mb-6">
      <div class="flex-1 relative">
        <Search size={18} class="absolute left-3 top-2.5 text-gray-400" />
        <input
          type="text"
          bind:value={searchQuery}
          on:input={handleSearch}
          placeholder="Search servers..."
          class="w-full pl-10 pr-4 py-2 border rounded focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
      </div>
      <select bind:value={environmentFilter} on:change={handleSearch} class="px-3 py-2 border rounded">
        <option value="">All Environments</option>
        <option value="production">Production</option>
        <option value="staging">Staging</option>
        <option value="development">Development</option>
      </select>
      <select bind:value={regionFilter} on:change={handleSearch} class="px-3 py-2 border rounded">
        <option value="">All Regions</option>
        <option value="indonesia">Indonesia</option>
        <option value="singapore">Singapore</option>
        <option value="japan">Japan</option>
      </select>
    </div>

    <!-- Server list -->
    {#if $serverStore.loading}
      <p class="text-gray-500">Loading...</p>
    {:else if $serverStore.servers.length === 0}
      <p class="text-gray-500">No servers found. Add one to get started.</p>
    {:else}
      <div class="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
        {#each $serverStore.servers as server}
          <a href="/servers/{server.id}" class="block p-4 bg-white rounded-lg border hover:shadow transition">
            <div class="flex items-start justify-between mb-2">
              <div class="flex items-center gap-2">
                <Server size={18} class="text-gray-400" />
                <span class="font-medium">{server.name}</span>
              </div>
              <button
                on:click|preventDefault={() => toggleFavorite(server.id)}
                class="text-gray-300 hover:text-yellow-400"
              >
                <Star size={18} fill={server.favorite ? 'currentColor' : 'none'} class={server.favorite ? 'text-yellow-400' : ''} />
              </button>
            </div>
            <p class="text-sm text-gray-500 mb-2">{server.host}:{server.port}</p>
            <div class="flex gap-2">
              {#if server.environment}
                <span class="px-2 py-0.5 text-xs rounded-full {envColor(server.environment)}">{server.environment}</span>
              {/if}
              {#if server.region}
                <span class="px-2 py-0.5 text-xs rounded-full bg-blue-100 text-blue-700">{server.region}</span>
              {/if}
            </div>
          </a>
        {/each}
      </div>
    {/if}
  </div>
</div>
```

- [ ] **Step 2: Commit**

```bash
git add web/src/routes/servers/+page.svelte
git commit -m "feat: add server list page with filters and search"
```

---

### Task 9: Server Detail Page

**Files:**
- Create: `web/src/routes/servers/[id]/+page.svelte`

- [ ] **Step 1: Write server detail page**

```svelte
<!-- web/src/routes/servers/[id]/+page.svelte -->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { page } from '$app/stores';
  import { api } from '$lib/api/client';
  import { wsConnect, type WSMessage } from '$lib/api/websocket';
  import type { Server, ServerInfo } from '$lib/stores/servers';
  import { ArrowLeft, Play, Cpu, MemoryStick, HardDrive, Network, Clock } from 'lucide-svelte';

  let server: Server | null = null;
  let info: ServerInfo | null = null;
  let loading = true;
  let connecting = false;
  let wsSteps: { step: string; status: string; value?: string; error?: string }[] = [];
  let ws: WebSocket | null = null;

  const serverId = parseInt($page.params.id);

  onMount(async () => {
    try {
      server = await api.get<Server>(`/servers/${serverId}`);
    } catch {
      // handle error
    } finally {
      loading = false;
    }
  });

  onDestroy(() => {
    ws?.close();
  });

  function handleConnect() {
    connecting = true;
    wsSteps = [];

    ws = wsConnect(
      serverId,
      (msg: WSMessage) => {
        if (msg.step === 'done') {
          connecting = false;
          // Fetch cached info
          loadInfo();
        } else {
          wsSteps = [...wsSteps, {
            step: msg.step,
            status: msg.status,
            value: msg.value !== undefined ? String(msg.value) : undefined,
            error: msg.error,
          }];
        }
      },
      () => { connecting = false; },
      () => { connecting = false; }
    );
  }

  async function loadInfo() {
    try {
      info = await api.get<ServerInfo>(`/servers/${serverId}/info`);
    } catch {
      // info not available yet
    }
  }
</script>

<div class="min-h-screen bg-gray-50">
  <header class="bg-white border-b px-6 py-4">
    <a href="/servers" class="flex items-center gap-2 text-sm text-gray-600 hover:text-gray-900 mb-2">
      <ArrowLeft size={16} /> Back to Servers
    </a>
    {#if server}
      <div class="flex items-center justify-between">
        <div>
          <h1 class="text-xl font-bold">{server.name}</h1>
          <p class="text-sm text-gray-500">{server.host}:{server.port} · {server.username}</p>
        </div>
        <button
          on:click={handleConnect}
          disabled={connecting}
          class="flex items-center gap-1 px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50"
        >
          <Play size={18} /> {connecting ? 'Connecting...' : 'Connect'}
        </button>
      </div>
    {/if}
  </header>

  <div class="max-w-4xl mx-auto p-6">
    {#if loading}
      <p class="text-gray-500">Loading...</p>
    {:else if !server}
      <p class="text-red-500">Server not found</p>
    {:else}
      <!-- Connection test progress -->
      {#if wsSteps.length > 0}
        <div class="mb-6">
          <h2 class="text-sm font-semibold mb-3">Connection Test</h2>
          <div class="space-y-2">
            {#each wsSteps as step}
              <div class="flex items-center gap-2 text-sm">
                {#if step.status === 'success'}
                  <span class="w-2 h-2 rounded-full bg-green-500"></span>
                {:else}
                  <span class="w-2 h-2 rounded-full bg-red-500"></span>
                {/if}
                <span class="font-mono">{step.step}</span>
                {#if step.value}
                  <span class="text-gray-500">→ {step.value}</span>
                {/if}
                {#if step.error}
                  <span class="text-red-500">→ {step.error}</span>
                {/if}
              </div>
            {/each}
          </div>
        </div>
      {/if}

      <!-- System info -->
      {#if info}
        <div class="grid grid-cols-2 gap-4">
          <div class="p-4 bg-white rounded-lg border">
            <div class="flex items-center gap-2 mb-2"><Cpu size={18} class="text-gray-400" /> <span class="text-sm font-semibold">CPU</span></div>
            <p class="text-sm text-gray-600">{info.cpuModel || 'Unknown'}</p>
            <p class="text-sm text-gray-500">{info.cpuCores} cores</p>
          </div>
          <div class="p-4 bg-white rounded-lg border">
            <div class="flex items-center gap-2 mb-2"><MemoryStick size={18} class="text-gray-400" /> <span class="text-sm font-semibold">RAM</span></div>
            <p class="text-sm text-gray-600">{info.ramTotalMb} MB</p>
          </div>
          <div class="p-4 bg-white rounded-lg border">
            <div class="flex items-center gap-2 mb-2"><HardDrive size={18} class="text-gray-400" /> <span class="text-sm font-semibold">Disk</span></div>
            <p class="text-sm text-gray-600">{info.diskTotalGb} GB</p>
          </div>
          <div class="p-4 bg-white rounded-lg border">
            <div class="flex items-center gap-2 mb-2"><Network size={18} class="text-gray-400" /> <span class="text-sm font-semibold">Network</span></div>
            <p class="text-sm text-gray-600">{info.publicIp || 'Unknown'}</p>
            <p class="text-sm text-gray-500">{info.privateIp || 'Unknown'}</p>
          </div>
          <div class="p-4 bg-white rounded-lg border">
            <div class="flex items-center gap-2 mb-2"><Clock size={18} class="text-gray-400" /> <span class="text-sm font-semibold">System</span></div>
            <p class="text-sm text-gray-600">{info.os}</p>
            <p class="text-sm text-gray-500">Kernel {info.kernel} · {info.architecture}</p>
          </div>
          <div class="p-4 bg-white rounded-lg border">
            <span class="text-sm font-semibold">Virtualization</span>
            <p class="text-sm text-gray-600">{info.virtualization || 'Unknown'}</p>
            <p class="text-sm text-gray-500">Latency: {info.latencyMs}ms</p>
          </div>
        </div>
      {/if}
    {/if}
  </div>
</div>
```

- [ ] **Step 2: Commit**

```bash
git add web/src/routes/servers/[id]/+page.svelte
git commit -m "feat: add server detail page with connection test"
```

---

### Task 10: Add/Edit Server Form

**Files:**
- Create: `web/src/routes/servers/new/+page.svelte`
- Create: `web/src/routes/servers/[id]/edit/+page.svelte`

- [ ] **Step 1: Write add server page**

```svelte
<!-- web/src/routes/servers/new/+page.svelte -->
<script lang="ts">
  import { goto } from '$app/navigation';
  import { createServer } from '$lib/stores/servers';
  import { ArrowLeft } from 'lucide-svelte';

  let name = '';
  let description = '';
  let host = '';
  let port = 22;
  let username = 'root';
  let password = '';
  let authMethod = 'password';
  let sshKey = '';
  let passphrase = '';
  let tags = '';
  let environment = '';
  let region = '';
  let color = '#3b82f6';
  let error = '';
  let loading = false;

  async function handleSubmit() {
    error = '';
    if (!name || !host || !username) {
      error = 'Name, host, and username are required';
      return;
    }
    loading = true;
    try {
      const data: Record<string, unknown> = {
        name, description, host, port: Number(port), username,
        tags: tags ? tags.split(',').map((t) => t.trim()) : [],
        environment, region, color,
      };
      if (authMethod === 'password') {
        data.password = password;
      } else {
        data.sshKey = sshKey;
        data.passphrase = passphrase;
      }
      await createServer(data);
      goto('/servers');
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  }
</script>

<div class="min-h-screen bg-gray-50">
  <header class="bg-white border-b px-6 py-4">
    <a href="/servers" class="flex items-center gap-2 text-sm text-gray-600 hover:text-gray-900">
      <ArrowLeft size={16} /> Back to Servers
    </a>
    <h1 class="text-xl font-bold mt-2">Add Server</h1>
  </header>

  <div class="max-w-2xl mx-auto p-6">
    {#if error}
      <div class="mb-4 p-3 bg-red-50 text-red-700 rounded">{error}</div>
    {/if}

    <form on:submit|preventDefault={handleSubmit} class="space-y-4">
      <div>
        <label class="block text-sm font-medium mb-1">Name *</label>
        <input bind:value={name} class="w-full px-3 py-2 border rounded" placeholder="Web Server 01" />
      </div>
      <div>
        <label class="block text-sm font-medium mb-1">Description</label>
        <input bind:value={description} class="w-full px-3 py-2 border rounded" placeholder="Main production web server" />
      </div>
      <div class="grid grid-cols-3 gap-4">
        <div class="col-span-2">
          <label class="block text-sm font-medium mb-1">Host/IP *</label>
          <input bind:value={host} class="w-full px-3 py-2 border rounded" placeholder="192.168.1.100" />
        </div>
        <div>
          <label class="block text-sm font-medium mb-1">Port</label>
          <input type="number" bind:value={port} class="w-full px-3 py-2 border rounded" />
        </div>
      </div>
      <div>
        <label class="block text-sm font-medium mb-1">Username *</label>
        <input bind:value={username} class="w-full px-3 py-2 border rounded" placeholder="root" />
      </div>

      <div>
        <label class="block text-sm font-medium mb-1">Authentication</label>
        <div class="flex gap-4">
          <label class="flex items-center gap-2">
            <input type="radio" bind:group={authMethod} value="password" /> Password
          </label>
          <label class="flex items-center gap-2">
            <input type="radio" bind:group={authMethod} value="key" /> SSH Key
          </label>
        </div>
      </div>

      {#if authMethod === 'password'}
        <div>
          <label class="block text-sm font-medium mb-1">Password</label>
          <input type="password" bind:value={password} class="w-full px-3 py-2 border rounded" />
        </div>
      {:else}
        <div>
          <label class="block text-sm font-medium mb-1">Private Key</label>
          <textarea bind:value={sshKey} class="w-full px-3 py-2 border rounded font-mono text-sm" rows="6" placeholder="-----BEGIN RSA PRIVATE KEY-----"></textarea>
        </div>
        <div>
          <label class="block text-sm font-medium mb-1">Passphrase</label>
          <input type="password" bind:value={passphrase} class="w-full px-3 py-2 border rounded" />
        </div>
      {/if}

      <div>
        <label class="block text-sm font-medium mb-1">Tags (comma-separated)</label>
        <input bind:value={tags} class="w-full px-3 py-2 border rounded" placeholder="web, nginx, production" />
      </div>

      <div class="grid grid-cols-2 gap-4">
        <div>
          <label class="block text-sm font-medium mb-1">Environment</label>
          <select bind:value={environment} class="w-full px-3 py-2 border rounded">
            <option value="">None</option>
            <option value="production">Production</option>
            <option value="staging">Staging</option>
            <option value="development">Development</option>
          </select>
        </div>
        <div>
          <label class="block text-sm font-medium mb-1">Region</label>
          <select bind:value={region} class="w-full px-3 py-2 border rounded">
            <option value="">None</option>
            <option value="indonesia">Indonesia</option>
            <option value="singapore">Singapore</option>
            <option value="japan">Japan</option>
          </select>
        </div>
      </div>

      <div>
        <label class="block text-sm font-medium mb-1">Color</label>
        <input type="color" bind:value={color} class="w-16 h-10 border rounded" />
      </div>

      <button type="submit" disabled={loading} class="px-6 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50">
        {loading ? 'Adding...' : 'Add Server'}
      </button>
    </form>
  </div>
</div>
```

- [ ] **Step 2: Write edit server page (same form, pre-filled)**

```svelte
<!-- web/src/routes/servers/[id]/edit/+page.svelte -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { api } from '$lib/api/client';
  import { updateServer } from '$lib/stores/servers';
  import { ArrowLeft } from 'lucide-svelte';
  import type { Server } from '$lib/stores/servers';

  let server: Server | null = null;
  let name = '';
  let description = '';
  let host = '';
  let port = 22;
  let username = 'root';
  let password = '';
  let tags = '';
  let environment = '';
  let region = '';
  let color = '#3b82f6';
  let error = '';
  let loading = false;
  let loadingServer = true;

  const serverId = parseInt($page.params.id);

  onMount(async () => {
    try {
      server = await api.get<Server>(`/servers/${serverId}`);
      name = server.name;
      description = server.description || '';
      host = server.host;
      port = server.port;
      username = server.username;
      tags = server.tags?.join(', ') || '';
      environment = server.environment || '';
      region = server.region || '';
      color = server.color || '#3b82f6';
    } catch {
      error = 'Failed to load server';
    } finally {
      loadingServer = false;
    }
  });

  async function handleSubmit() {
    error = '';
    loading = true;
    try {
      const data: Record<string, unknown> = {
        name, description, host, port: Number(port), username,
        tags: tags ? tags.split(',').map((t) => t.trim()) : [],
        environment, region, color,
      };
      if (password) {
        data.password = password;
      }
      await updateServer(serverId, data);
      goto(`/servers/${serverId}`);
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  }
</script>

<div class="min-h-screen bg-gray-50">
  <header class="bg-white border-b px-6 py-4">
    <a href="/servers/{serverId}" class="flex items-center gap-2 text-sm text-gray-600 hover:text-gray-900">
      <ArrowLeft size={16} /> Back to Server
    </a>
    <h1 class="text-xl font-bold mt-2">Edit Server</h1>
  </header>

  <div class="max-w-2xl mx-auto p-6">
    {#if loadingServer}
      <p class="text-gray-500">Loading...</p>
    {:else}
      {#if error}
        <div class="mb-4 p-3 bg-red-50 text-red-700 rounded">{error}</div>
      {/if}

      <form on:submit|preventDefault={handleSubmit} class="space-y-4">
        <div>
          <label class="block text-sm font-medium mb-1">Name *</label>
          <input bind:value={name} class="w-full px-3 py-2 border rounded" />
        </div>
        <div>
          <label class="block text-sm font-medium mb-1">Description</label>
          <input bind:value={description} class="w-full px-3 py-2 border rounded" />
        </div>
        <div class="grid grid-cols-3 gap-4">
          <div class="col-span-2">
            <label class="block text-sm font-medium mb-1">Host/IP *</label>
            <input bind:value={host} class="w-full px-3 py-2 border rounded" />
          </div>
          <div>
            <label class="block text-sm font-medium mb-1">Port</label>
            <input type="number" bind:value={port} class="w-full px-3 py-2 border rounded" />
          </div>
        </div>
        <div>
          <label class="block text-sm font-medium mb-1">Username *</label>
          <input bind:value={username} class="w-full px-3 py-2 border rounded" />
        </div>
        <div>
          <label class="block text-sm font-medium mb-1">New Password (leave blank to keep)</label>
          <input type="password" bind:value={password} class="w-full px-3 py-2 border rounded" />
        </div>
        <div>
          <label class="block text-sm font-medium mb-1">Tags (comma-separated)</label>
          <input bind:value={tags} class="w-full px-3 py-2 border rounded" />
        </div>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="block text-sm font-medium mb-1">Environment</label>
            <select bind:value={environment} class="w-full px-3 py-2 border rounded">
              <option value="">None</option>
              <option value="production">Production</option>
              <option value="staging">Staging</option>
              <option value="development">Development</option>
            </select>
          </div>
          <div>
            <label class="block text-sm font-medium mb-1">Region</label>
            <select bind:value={region} class="w-full px-3 py-2 border rounded">
              <option value="">None</option>
              <option value="indonesia">Indonesia</option>
              <option value="singapore">Singapore</option>
              <option value="japan">Japan</option>
            </select>
          </div>
        </div>
        <div>
          <label class="block text-sm font-medium mb-1">Color</label>
          <input type="color" bind:value={color} class="w-16 h-10 border rounded" />
        </div>
        <button type="submit" disabled={loading} class="px-6 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50">
          {loading ? 'Saving...' : 'Save Changes'}
        </button>
      </form>
    {/if}
  </div>
</div>
```

- [ ] **Step 3: Commit**

```bash
git add web/src/routes/servers/new/+page.svelte web/src/routes/servers/[id]/edit/+page.svelte
git commit -m "feat: add server create and edit pages"
```

---

### Task 11: Settings Page

**Files:**
- Create: `web/src/routes/settings/+page.svelte`

- [ ] **Step 1: Write settings page**

```svelte
<!-- web/src/routes/settings/+page.svelte -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '$lib/api/client';
  import { ArrowLeft, Copy, Check } from 'lucide-svelte';

  let publicKey = '';
  let loading = true;
  let copied = false;
  let regenerating = false;

  onMount(async () => {
    try {
      const res = await api.get<{ publicKey: string }>('/ssh-key/public');
      publicKey = res.publicKey;
    } catch {
      // key not generated yet
    } finally {
      loading = false;
    }
  });

  async function copyKey() {
    await navigator.clipboard.writeText(publicKey);
    copied = true;
    setTimeout(() => (copied = false), 2000);
  }

  async function regenerate() {
    if (!confirm('Regenerate SSH key pair? All servers using the old key will need re-authentication.')) return;
    regenerating = true;
    try {
      const res = await api.post<{ publicKey: string }>('/ssh-key/regenerate');
      publicKey = res.publicKey;
    } catch {
      // error
    } finally {
      regenerating = false;
    }
  }
</script>

<div class="min-h-screen bg-gray-50">
  <header class="bg-white border-b px-6 py-4">
    <a href="/servers" class="flex items-center gap-2 text-sm text-gray-600 hover:text-gray-900">
      <ArrowLeft size={16} /> Back to Servers
    </a>
    <h1 class="text-xl font-bold mt-2">Settings</h1>
  </header>

  <div class="max-w-2xl mx-auto p-6">
    <div class="mb-8">
      <h2 class="text-lg font-semibold mb-2">SSH Key Pair</h2>
      <p class="text-sm text-gray-500 mb-4">
        This key pair is used for passwordless SSH authentication. When you connect to a server
        with a password, Meshium offers to install this public key automatically.
      </p>

      {#if loading}
        <p class="text-gray-500">Loading...</p>
      {:else}
        <div class="p-4 bg-gray-50 rounded-lg border">
          <div class="flex items-center justify-between mb-2">
            <span class="text-sm font-medium">Public Key</span>
            <button on:click={copyKey} class="text-sm text-blue-600 hover:text-blue-700 flex items-center gap-1">
              {#if copied}<Check size={14} /> Copied{:else}<Copy size={14} /> Copy{/if}
            </button>
          </div>
          <pre class="text-xs font-mono text-gray-600 overflow-x-auto whitespace-pre-wrap">{publicKey || 'No key generated'}</pre>
        </div>
        <button
          on:click={regenerate}
          disabled={regenerating}
          class="mt-4 px-4 py-2 border border-red-300 text-red-600 rounded hover:bg-red-50 disabled:opacity-50"
        >
          {regenerating ? 'Regenerating...' : 'Regenerate Key Pair'}
        </button>
      {/if}
    </div>
  </div>
</div>
```

- [ ] **Step 2: Commit**

```bash
git add web/src/routes/settings/+page.svelte
git commit -m "feat: add settings page with SSH key management"
```

---

### Task 12: Go Embed for Production Build

**Files:**
- Modify: `cmd/server/main.go`
- Create: `cmd/server/embed.go`

- [ ] **Step 1: Write embed file**

```go
// cmd/server/embed.go
package main

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:web/build
var webFS embed.FS

func staticHandler() http.Handler {
	sub, _ := fs.Sub(webFS, "web/build")
	return http.FileServer(http.FS(sub))
}
```

- [ ] **Step 2: Update main.go to serve static files**

Add to `main()` in `cmd/server/main.go`, before `http.ListenAndServe`:

```go
	// Serve frontend (production)
	mux.Handle("/", staticHandler())
```

- [ ] **Step 3: Verify it compiles (will fail if web/build doesn't exist, which is expected in dev)**

```bash
# In development, the frontend is served by Vite dev server
# In production, build the frontend first:
cd web && npm run build && cd ..
go build ./cmd/server/
```

- [ ] **Step 4: Commit**

```bash
git add cmd/server/embed.go cmd/server/main.go
git commit -m "feat: add Go embed for serving frontend in production"
```
