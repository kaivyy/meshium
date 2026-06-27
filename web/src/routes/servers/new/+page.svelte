<script lang="ts">
  import { goto } from '$app/navigation';
  import { ArrowLeft } from 'lucide-svelte';
  import { createServer } from '$lib/stores/servers';

  let name = '';
  let description = '';
  let host = '';
  let port = 22;
  let username = 'root';
  let authMethod: 'password' | 'key' = 'password';
  let password = '';
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

    if (authMethod === 'password' && !password) {
      error = 'Password is required when using password authentication';
      return;
    }

    if (authMethod === 'key' && !sshKey) {
      error = 'SSH key is required when using key authentication';
      return;
    }

    loading = true;

    try {
      const data: Record<string, unknown> = {
        name,
        description,
        host,
        port: Number(port),
        username,
        tags: tags
          ? tags
              .split(',')
              .map((tag) => tag.trim())
              .filter(Boolean)
          : [],
        environment,
        region,
        color
      };

      if (authMethod === 'password') {
        data.password = password;
      } else {
        data.sshKey = sshKey;
        data.passphrase = passphrase;
      }

      await createServer(data);
      goto('/');
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to add server';
    } finally {
      loading = false;
    }
  }
</script>

<div class="min-h-screen bg-slate-50">
  <header class="border-b border-slate-200 bg-white px-6 py-4">
    <a href="/" class="inline-flex items-center gap-2 text-sm text-slate-600 transition hover:text-slate-900">
      <ArrowLeft size={16} /> Back to Servers
    </a>
    <h1 class="mt-2 text-xl font-bold tracking-tight text-slate-900">Add Server</h1>
  </header>

  <main class="mx-auto w-full max-w-3xl p-6">
    {#if error}
      <div class="mb-4 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{error}</div>
    {/if}

    <form class="space-y-5 rounded-2xl border border-slate-200 bg-white p-6 shadow-sm" on:submit|preventDefault={handleSubmit}>
      <div class="grid gap-5 md:grid-cols-2">
        <div class="md:col-span-2">
          <label class="mb-1 block text-sm font-medium text-slate-700">Name *</label>
          <input
            bind:value={name}
            class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
            placeholder="Web Server 01"
          />
        </div>

        <div class="md:col-span-2">
          <label class="mb-1 block text-sm font-medium text-slate-700">Notes</label>
          <textarea
            bind:value={description}
            rows="4"
            class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
            placeholder="Main production web server"
          ></textarea>
        </div>

        <div class="md:col-span-1">
          <label class="mb-1 block text-sm font-medium text-slate-700">Host / IP *</label>
          <input
            bind:value={host}
            class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
            placeholder="192.168.1.100"
          />
        </div>

        <div>
          <label class="mb-1 block text-sm font-medium text-slate-700">Port</label>
          <input
            type="number"
            bind:value={port}
            class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
            min="1"
          />
        </div>

        <div class="md:col-span-2">
          <label class="mb-1 block text-sm font-medium text-slate-700">Username *</label>
          <input
            bind:value={username}
            class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
            placeholder="root"
          />
        </div>

        <div class="md:col-span-2">
          <label class="mb-2 block text-sm font-medium text-slate-700">Authentication</label>
          <div class="flex flex-wrap gap-4">
            <label class="inline-flex items-center gap-2 text-sm text-slate-700">
              <input type="radio" bind:group={authMethod} value="password" />
              Password
            </label>
            <label class="inline-flex items-center gap-2 text-sm text-slate-700">
              <input type="radio" bind:group={authMethod} value="key" />
              SSH Key
            </label>
          </div>
        </div>

        {#if authMethod === 'password'}
          <div class="md:col-span-2">
            <label class="mb-1 block text-sm font-medium text-slate-700">Password</label>
            <input
              type="password"
              bind:value={password}
              class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
              placeholder="SSH password"
            />
          </div>
        {:else}
          <div class="md:col-span-2">
            <label class="mb-1 block text-sm font-medium text-slate-700">SSH Key</label>
            <textarea
              bind:value={sshKey}
              rows="6"
              class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 font-mono text-sm text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
              placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
            ></textarea>
          </div>

          <div class="md:col-span-2">
            <label class="mb-1 block text-sm font-medium text-slate-700">Passphrase</label>
            <input
              type="password"
              bind:value={passphrase}
              class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
              placeholder="Optional passphrase"
            />
          </div>
        {/if}

        <div class="md:col-span-2">
          <label class="mb-1 block text-sm font-medium text-slate-700">Tags</label>
          <input
            bind:value={tags}
            class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
            placeholder="web, nginx, production"
          />
        </div>

        <div>
          <label class="mb-1 block text-sm font-medium text-slate-700">Environment</label>
          <select
            bind:value={environment}
            class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
          >
            <option value="">None</option>
            <option value="production">Production</option>
            <option value="staging">Staging</option>
            <option value="development">Development</option>
          </select>
        </div>

        <div>
          <label class="mb-1 block text-sm font-medium text-slate-700">Region</label>
          <select
            bind:value={region}
            class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
          >
            <option value="">None</option>
            <option value="indonesia">Indonesia</option>
            <option value="singapore">Singapore</option>
            <option value="japan">Japan</option>
          </select>
        </div>

        <div>
          <label class="mb-1 block text-sm font-medium text-slate-700">Color</label>
          <input type="color" bind:value={color} class="h-10 w-16 rounded border border-slate-300 bg-white" />
        </div>
      </div>

      <div class="flex flex-wrap items-center gap-3">
        <button
          type="submit"
          disabled={loading}
          class="inline-flex items-center justify-center rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
        >
          {loading ? 'Adding...' : 'Add Server'}
        </button>
        <a href="/" class="text-sm font-medium text-slate-600 transition hover:text-slate-900">Cancel</a>
      </div>
    </form>
  </main>
</div>
