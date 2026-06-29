<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { ArrowLeft } from 'lucide-svelte';
  import { createServer, type Server } from '$lib/stores/servers';
  import { api } from '$lib/api/client';

  let name = '';
  let description = '';
  let host = '';
  let port = 22;
  let username = 'root';
  let authMethod: 'password' | 'key' = 'password';
  let password = '';
  let sshKey = '';
  let passphrase = '';
  let keyType: 'rsa' | 'ed25519' | 'ecdsa' = 'ed25519';
  let tags = '';
  let environment = '';
  let region = '';
  let color = '#3b82f6';
  let bastionId = 0;
  let availableServers: Server[] = [];
  let error = '';
  let loading = false;

  onMount(async () => {
    try {
      availableServers = await api.get('/servers') as Server[];
    } catch {
      // ignore
    }
  });

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
        color,
        bastionId: bastionId,
        keyType: keyType
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

<div class="p-4 sm:p-6">
  <a href="/" class="inline-flex items-center gap-2 text-sm text-slate-600 transition hover:text-slate-900">
    <ArrowLeft size={16} /> Back to Servers
  </a>
  <h1 class="mt-2 text-xl font-bold tracking-tight text-slate-900">Add Server</h1>

  <div class="mt-4 max-w-3xl mx-auto">
    {#if error}
      <div class="mb-4 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{error}</div>
    {/if}

    <form class="space-y-5 rounded-2xl border border-slate-200 bg-white p-4 sm:p-6 shadow-sm" on:submit|preventDefault={handleSubmit}>
      <div class="grid gap-5 md:grid-cols-2">
        <div class="md:col-span-2">
          <label for="name" class="mb-1 block text-sm font-medium text-slate-700">Name *</label>
          <input
            id="name"
            bind:value={name}
            class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
            placeholder="Web Server 01"
          />
        </div>

        <div class="md:col-span-2">
          <label for="description" class="mb-1 block text-sm font-medium text-slate-700">Notes</label>
          <textarea
            id="description"
            bind:value={description}
            rows="4"
            class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
            placeholder="Main production web server"
          ></textarea>
        </div>

        <div class="md:col-span-1">
          <label for="host" class="mb-1 block text-sm font-medium text-slate-700">Host / IP *</label>
          <input
            id="host"
            bind:value={host}
            class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
            placeholder="192.168.1.100"
          />
        </div>

        <div>
          <label for="port" class="mb-1 block text-sm font-medium text-slate-700">Port</label>
          <input
            id="port"
            type="number"
            bind:value={port}
            class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
            min="1"
          />
        </div>

        <div class="md:col-span-2">
          <label for="username" class="mb-1 block text-sm font-medium text-slate-700">Username *</label>
          <input
            id="username"
            bind:value={username}
            class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
            placeholder="root"
          />
        </div>

        <div class="md:col-span-2">
          <span class="mb-2 block text-sm font-medium text-slate-700">Authentication</span>
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
            <label for="password" class="mb-1 block text-sm font-medium text-slate-700">Password</label>
            <input
              id="password"
              type="password"
              bind:value={password}
              class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
              placeholder="SSH password"
            />
          </div>
        {:else}
          <div class="md:col-span-2">
            <label for="keyType" class="mb-1 block text-sm font-medium text-slate-700">Key Type</label>
            <select
              id="keyType"
              bind:value={keyType}
              class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
            >
              <option value="ed25519">ED25519 (Recommended)</option>
              <option value="rsa">RSA 4096-bit</option>
              <option value="ecdsa">ECDSA 521-bit</option>
            </select>
            <p class="mt-1 text-xs text-slate-500">ED25519 is recommended for new keys — faster and more secure than RSA</p>
          </div>

          <div class="md:col-span-2">
            <label for="sshKey" class="mb-1 block text-sm font-medium text-slate-700">SSH Key</label>
            <textarea
              id="sshKey"
              bind:value={sshKey}
              rows="6"
              class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 font-mono text-sm text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
              placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
            ></textarea>
          </div>

          <div class="md:col-span-2">
            <label for="passphrase" class="mb-1 block text-sm font-medium text-slate-700">Passphrase</label>
            <input
              id="passphrase"
              type="password"
              bind:value={passphrase}
              class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
              placeholder="Optional passphrase"
            />
          </div>
        {/if}

        <div class="md:col-span-2">
          <label for="bastion" class="mb-1 block text-sm font-medium text-slate-700">Bastion / Jump Host</label>
          <select
            id="bastion"
            bind:value={bastionId}
            class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
          >
            <option value={0}>None (Direct Connection)</option>
            {#each availableServers as srv}
              <option value={srv.id}>{srv.name} ({srv.host}:{srv.port})</option>
            {/each}
          </select>
          <p class="mt-1 text-xs text-slate-500">Route SSH connections through this server as a jump host</p>
        </div>

        <div class="md:col-span-2">
          <label for="tags" class="mb-1 block text-sm font-medium text-slate-700">Tags</label>
          <input
            id="tags"
            bind:value={tags}
            class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
            placeholder="web, nginx, production"
          />
        </div>

        <div>
          <label for="environment" class="mb-1 block text-sm font-medium text-slate-700">Environment</label>
          <select
            id="environment"
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
          <label for="region" class="mb-1 block text-sm font-medium text-slate-700">Region</label>
          <select
            id="region"
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
          <label for="color" class="mb-1 block text-sm font-medium text-slate-700">Color</label>
          <input id="color" type="color" bind:value={color} class="h-10 w-16 rounded border border-slate-300 bg-white" />
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
  </div>
</div>
