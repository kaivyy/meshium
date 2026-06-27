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
  let tags = '';
  let environment = '';
  let region = '';
  let color = '#3b82f6';
  let error = '';
  let loading = false;
  let loadingServer = true;

  const serverId = parseInt($page.params.id, 10);

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
      authMethod = server.authMethod === 'key' ? 'key' : 'password';
      initialAuthMethod = authMethod;
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
        color
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

<div class="min-h-screen bg-slate-50">
  <header class="border-b border-slate-200 bg-white px-6 py-4">
    <a href={`/servers/${serverId}`} class="inline-flex items-center gap-2 text-sm text-slate-600 transition hover:text-slate-900">
      <ArrowLeft size={16} /> Back to Server
    </a>
    <h1 class="mt-2 text-xl font-bold tracking-tight text-slate-900">Edit Server</h1>
  </header>

  <main class="mx-auto w-full max-w-3xl p-6">
    {#if loadingServer}
      <div class="rounded-lg border border-dashed border-slate-300 bg-white px-4 py-8 text-center text-slate-500">
        Loading server...
      </div>
    {:else}
      {#if error}
        <div class="mb-4 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{error}</div>
      {/if}

      {#if server}
        <form class="space-y-5 rounded-2xl border border-slate-200 bg-white p-6 shadow-sm" on:submit|preventDefault={handleSubmit}>
          <div class="grid gap-5 md:grid-cols-2">
            <div class="md:col-span-2">
              <label class="mb-1 block text-sm font-medium text-slate-700">Name *</label>
              <input
                bind:value={name}
                class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
              />
            </div>

            <div class="md:col-span-2">
              <label class="mb-1 block text-sm font-medium text-slate-700">Notes</label>
              <textarea
                bind:value={description}
                rows="4"
                class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
              ></textarea>
            </div>

            <div class="md:col-span-1">
              <label class="mb-1 block text-sm font-medium text-slate-700">Host / IP *</label>
              <input
                bind:value={host}
                class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
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
                <label class="mb-1 block text-sm font-medium text-slate-700">New Password</label>
                <input
                  type="password"
                  bind:value={password}
                  class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
                  placeholder="Leave blank to keep the existing password"
                />
              </div>
            {:else}
              <div class="md:col-span-2">
                <label class="mb-1 block text-sm font-medium text-slate-700">SSH Key</label>
                <textarea
                  bind:value={sshKey}
                  rows="6"
                  class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 font-mono text-sm text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
                  placeholder="Leave blank to keep the existing SSH key"
                ></textarea>
              </div>

              <div class="md:col-span-2">
                <label class="mb-1 block text-sm font-medium text-slate-700">Passphrase</label>
                <input
                  type="password"
                  bind:value={passphrase}
                  class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
                  placeholder="Leave blank to keep the existing passphrase"
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
              {loading ? 'Saving...' : 'Save Changes'}
            </button>
            <a href={`/servers/${serverId}`} class="text-sm font-medium text-slate-600 transition hover:text-slate-900">Cancel</a>
          </div>
        </form>
      {/if}
    {/if}
  </main>
</div>
