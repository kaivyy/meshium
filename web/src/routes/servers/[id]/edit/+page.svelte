<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { ArrowLeft } from 'lucide-svelte';
  import { api } from '$lib/api/client';
  import { updateServer, type Server } from '$lib/stores/servers';

  let server: Server | null = null;
  let name = '';
  let description = '';
  let host = '';
  let port = 22;
  let username = 'root';
  let authMethod: 'password' | 'key' = 'password';
  let initialAuthMethod: 'password' | 'key' = 'password';
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
  let loadingServer = true;

  const serverId = parseInt($page.params.id ?? '0', 10);

  onMount(async () => {
    try {
      server = await api.get(`/servers/${serverId}`) as Server;
      name = server.name;
      description = server.description || '';
      host = server.host;
      port = server.port;
      username = server.username;
      tags = server.tags?.join(', ') || '';
      environment = server.environment || '';
      region = server.region || '';
      color = server.color || '#3b82f6';
      bastionId = (server as any).bastionId || 0;
      authMethod = server.authMethod === 'key' ? 'key' : 'password';
      initialAuthMethod = authMethod;
      keyType = (server as any).keyType || 'ed25519';

      // Load available servers for bastion selection
      try {
        availableServers = await api.get('/servers') as Server[];
        // Filter out the current server from bastion options
        availableServers = availableServers.filter(s => s.id !== serverId);
      } catch {
        // ignore
      }
    } catch {
      error = 'Failed to load server';
    } finally {
      loadingServer = false;
    }
  });

  async function handleSubmit() {
    error = '';

    if (!name || !host || !username) {
      error = 'Name, host, and username are required';
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
        if (password) {
          data.password = password;
          if (initialAuthMethod === 'key') {
            data.sshKey = '';
            data.passphrase = '';
          }
        }
      } else if (authMethod === 'key') {
        if (sshKey) {
          data.sshKey = sshKey;
          if (initialAuthMethod === 'password') {
            data.password = '';
          }
        }
        if (passphrase) {
          data.passphrase = passphrase;
        }
      }

      await updateServer(serverId, data);
      goto(`/servers/${serverId}`);
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to save server';
    } finally {
      loading = false;
    }
  }
</script>

<div class="p-4 sm:p-6">
  <a href={`/servers/${serverId}`} class="inline-flex items-center gap-2 text-sm text-fg-muted transition hover:text-fg">
    <ArrowLeft size={16} /> Back to Server
  </a>
  <h1 class="mt-2 text-xl font-bold tracking-tight text-fg">Edit Server</h1>

  <div class="mt-4 max-w-3xl mx-auto">
    {#if loadingServer}
      <div class="rounded-lg border border-dashed border-border-strong bg-surface px-4 py-8 text-center text-fg-subtle">
        Loading server...
      </div>
    {:else}
      {#if error}
        <div class="mb-4 rounded-lg border border-error bg-error/10 px-4 py-3 text-sm text-error">{error}</div>
      {/if}

      {#if server}
        <form class="space-y-5 rounded-2xl border border-border bg-surface p-4 sm:p-6 shadow-sm" on:submit|preventDefault={handleSubmit}>
          <div class="grid gap-5 md:grid-cols-2">
            <div class="md:col-span-2">
              <label for="name" class="mb-1 block text-sm font-medium text-fg-muted">Name *</label>
              <input
                id="name"
                bind:value={name}
                class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
              />
            </div>

            <div class="md:col-span-2">
              <label for="description" class="mb-1 block text-sm font-medium text-fg-muted">Notes</label>
              <textarea
                id="description"
                bind:value={description}
                rows="4"
                class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
              ></textarea>
            </div>

            <div class="md:col-span-1">
              <label for="host" class="mb-1 block text-sm font-medium text-fg-muted">Host / IP *</label>
              <input
                id="host"
                bind:value={host}
                class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
              />
            </div>

            <div>
              <label for="port" class="mb-1 block text-sm font-medium text-fg-muted">Port</label>
              <input
                id="port"
                type="number"
                bind:value={port}
                class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
                min="1"
              />
            </div>

            <div class="md:col-span-2">
              <label for="username" class="mb-1 block text-sm font-medium text-fg-muted">Username *</label>
              <input
                id="username"
                bind:value={username}
                class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
              />
            </div>

            <div class="md:col-span-2">
              <span class="mb-2 block text-sm font-medium text-fg-muted">Authentication</span>
              <div class="flex flex-wrap gap-4">
                <label class="inline-flex items-center gap-2 text-sm text-fg-muted">
                  <input type="radio" bind:group={authMethod} value="password" />
                  Password
                </label>
                <label class="inline-flex items-center gap-2 text-sm text-fg-muted">
                  <input type="radio" bind:group={authMethod} value="key" />
                  SSH Key
                </label>
              </div>
            </div>

            {#if authMethod === 'password'}
              <div class="md:col-span-2">
                <label for="password" class="mb-1 block text-sm font-medium text-fg-muted">New Password</label>
                <input
                  id="password"
                  type="password"
                  bind:value={password}
                  class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
                  placeholder="Leave blank to keep the existing password"
                />
              </div>
            {:else}
              <div class="md:col-span-2">
                <label for="keyType" class="mb-1 block text-sm font-medium text-fg-muted">Key Type</label>
                <select
                  id="keyType"
                  bind:value={keyType}
                  class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
                >
                  <option value="ed25519">ED25519 (Recommended)</option>
                  <option value="rsa">RSA 4096-bit</option>
                  <option value="ecdsa">ECDSA 521-bit</option>
                </select>
                <p class="mt-1 text-xs text-fg-subtle">Used when generating or rotating keys</p>
              </div>

              <div class="md:col-span-2">
                <label for="sshKey" class="mb-1 block text-sm font-medium text-fg-muted">SSH Key</label>
                <textarea
                  id="sshKey"
                  bind:value={sshKey}
                  rows="6"
                  class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 font-mono text-sm text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
                  placeholder="Leave blank to keep the existing SSH key"
                ></textarea>
              </div>

              <div class="md:col-span-2">
                <label for="passphrase" class="mb-1 block text-sm font-medium text-fg-muted">Passphrase</label>
                <input
                  id="passphrase"
                  type="password"
                  bind:value={passphrase}
                  class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
                  placeholder="Leave blank to keep the existing passphrase"
                />
              </div>
            {/if}

            <div class="md:col-span-2">
              <label for="tags" class="mb-1 block text-sm font-medium text-fg-muted">Tags</label>
              <input
                id="tags"
                bind:value={tags}
                class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
                placeholder="web, nginx, production"
              />
            </div>

            <div>
              <label for="environment" class="mb-1 block text-sm font-medium text-fg-muted">Environment</label>
              <select
                id="environment"
                bind:value={environment}
                class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
              >
                <option value="">None</option>
                <option value="production">Production</option>
                <option value="staging">Staging</option>
                <option value="development">Development</option>
              </select>
            </div>

            <div>
              <label for="region" class="mb-1 block text-sm font-medium text-fg-muted">Region</label>
              <select
                id="region"
                bind:value={region}
                class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
              >
                <option value="">None</option>
                <option value="indonesia">Indonesia</option>
                <option value="singapore">Singapore</option>
                <option value="japan">Japan</option>
              </select>
            </div>

            <div class="md:col-span-2">
              <label for="bastion" class="mb-1 block text-sm font-medium text-fg-muted">Bastion / Jump Host</label>
              <select
                id="bastion"
                bind:value={bastionId}
                class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
              >
                <option value={0}>None (Direct Connection)</option>
                {#each availableServers as srv}
                  <option value={srv.id}>{srv.name} ({srv.host}:{srv.port})</option>
                {/each}
              </select>
              <p class="mt-1 text-xs text-fg-subtle">Route SSH connections through this server as a jump host</p>
            </div>

            <div>
              <label for="color" class="mb-1 block text-sm font-medium text-fg-muted">Color</label>
              <input id="color" type="color" bind:value={color} class="h-10 w-16 rounded border border-border-strong bg-surface" />
            </div>
          </div>

          <div class="flex flex-wrap items-center gap-3">
            <button
              type="submit"
              disabled={loading}
              class="inline-flex items-center justify-center rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-50"
            >
              {loading ? 'Saving...' : 'Save Changes'}
            </button>
            <a href={`/servers/${serverId}`} class="text-sm font-medium text-fg-muted transition hover:text-fg">Cancel</a>
          </div>
        </form>
      {/if}
    {/if}
  </div>
</div>
