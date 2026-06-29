<script lang="ts">
  import { onMount } from 'svelte';
  import { Plus, PencilLine, Trash2, Copy, ServerCog, Layers3 } from 'lucide-svelte';
  import { api } from '$lib/api/client';

  interface ConnectionProfile {
    id: number;
    name: string;
    description: string;
    timeoutSeconds: number;
    retryCount: number;
    retryDelayMs: number;
    backoffStrategy: string;
    keepaliveSeconds: number;
    reconnectEnabled: boolean;
    bufferSizeKb: number;
    compression: boolean;
    parallelism: number;
    isBuiltin: boolean;
  }

  let profiles: ConnectionProfile[] = [];
  let loading = true;
  let error = '';
  let saving = false;
  let deleting = false;
  let mode: 'view' | 'new' | 'edit' = 'new';
  let selectedProfileId: number | null = null;

  let profileId = 0;
  let name = '';
  let description = '';
  let timeoutSeconds = 30;
  let retryCount = 3;
  let retryDelayMs = 1000;
  let backoffStrategy = 'exponential';
  let keepaliveSeconds = 30;
  let reconnectEnabled = true;
  let bufferSizeKb = 64;
  let compression = false;
  let parallelism = 1;

  onMount(loadProfiles);

  async function loadProfiles() {
    loading = true;
    error = '';

    try {
      profiles = (await api.get('/connection-profiles')) as Array<ConnectionProfile>;
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to load connection profiles';
    } finally {
      loading = false;
    }
  }

  function resetForm(profile?: ConnectionProfile) {
    profileId = profile?.id ?? 0;
    name = profile?.name ?? '';
    description = profile?.description ?? '';
    timeoutSeconds = profile?.timeoutSeconds ?? 30;
    retryCount = profile?.retryCount ?? 3;
    retryDelayMs = profile?.retryDelayMs ?? 1000;
    backoffStrategy = profile?.backoffStrategy ?? 'exponential';
    keepaliveSeconds = profile?.keepaliveSeconds ?? 30;
    reconnectEnabled = profile?.reconnectEnabled ?? true;
    bufferSizeKb = profile?.bufferSizeKb ?? 64;
    compression = profile?.compression ?? false;
    parallelism = profile?.parallelism ?? 1;
  }

  function selectProfile(profile: ConnectionProfile) {
    selectedProfileId = profile.id;
    resetForm(profile);
    mode = profile.isBuiltin ? 'view' : 'edit';
  }

  function startNewProfile(seed?: ConnectionProfile) {
    selectedProfileId = null;
    resetForm(seed);
    profileId = 0;
    mode = 'new';
  }

  function cloneSelected(profile: ConnectionProfile) {
    startNewProfile({
      ...profile,
      id: 0,
      name: `${profile.name} Copy`,
      isBuiltin: false
    });
  }

  async function saveProfile() {
    error = '';

    if (!name.trim()) {
      error = 'Profile name is required';
      return;
    }

    saving = true;

    const payload = {
      id: profileId,
      name: name.trim(),
      description,
      timeoutSeconds: Number(timeoutSeconds),
      retryCount: Number(retryCount),
      retryDelayMs: Number(retryDelayMs),
      backoffStrategy,
      keepaliveSeconds: Number(keepaliveSeconds),
      reconnectEnabled,
      bufferSizeKb: Number(bufferSizeKb),
      compression,
      parallelism: Number(parallelism),
      isBuiltin: false
    };

    try {
      let savedId = profileId;
      if (mode === 'new') {
        const created = await api.post<ConnectionProfile>('/connection-profiles', payload);
        savedId = created.id;
      } else {
        await api.put('/connection-profiles/' + profileId, payload);
      }

      await loadProfiles();
      const updated = profiles.find((item) => item.id === savedId);
      if (updated) {
        selectProfile(updated);
      }
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to save profile';
    } finally {
      saving = false;
    }
  }

  async function deleteProfile() {
    if (mode !== 'edit' || !profileId) return;
    if (!confirm('Delete this custom connection profile?')) return;

    deleting = true;
    error = '';

    try {
      await api.delete(`/connection-profiles/${profileId}`);
      startNewProfile();
      await loadProfiles();
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to delete profile';
    } finally {
      deleting = false;
    }
  }

  function formatProfileSummary(profile: ConnectionProfile) {
    return [
      `${profile.timeoutSeconds}s timeout`,
      `${profile.retryCount} retries`,
      `${profile.keepaliveSeconds}s keepalive`,
      `${profile.parallelism}x parallelism`
    ].join(' · ');
  }

  $: builtinProfiles = profiles.filter((profile) => profile.isBuiltin);
  $: customProfiles = profiles.filter((profile) => !profile.isBuiltin);
  $: selectedProfile = profiles.find((profile) => profile.id === selectedProfileId) ?? null;
</script>

<svelte:head>
  <title>Connection Profiles</title>
</svelte:head>

<div class="p-6">
  <div class="mx-auto flex max-w-7xl flex-col gap-6">
    <div class="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
      <div class="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <div class="inline-flex items-center gap-2 rounded-full bg-blue-50 px-3 py-1 text-xs font-medium text-blue-700">
            <ServerCog size={14} /> SSH Profiles
          </div>
          <h1 class="mt-3 text-2xl font-bold tracking-tight text-slate-900">Connection Profiles</h1>
          <p class="mt-2 max-w-3xl text-sm text-slate-500">
            Configure timeouts, retry policies, keepalives, compression, and parallelism for SSH workflows.
          </p>
        </div>

        <div class="flex flex-wrap gap-3">
          <button
            type="button"
            on:click={() => startNewProfile()}
            class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700"
          >
            <Plus size={16} /> New Profile
          </button>
          <a href="/ssh" class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50">
            Back to Dashboard
          </a>
        </div>
      </div>

      <div class="mt-6 grid gap-4 md:grid-cols-3">
        <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
          <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Built-in Profiles</p>
          <p class="mt-2 text-2xl font-bold text-slate-900">{builtinProfiles.length}</p>
        </div>
        <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
          <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Custom Profiles</p>
          <p class="mt-2 text-2xl font-bold text-slate-900">{customProfiles.length}</p>
        </div>
        <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
          <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Selected Mode</p>
          <p class="mt-2 text-2xl font-bold text-slate-900">{mode}</p>
        </div>
      </div>
    </div>

    {#if error}
      <div class="rounded-2xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700" role="alert">{error}</div>
    {/if}

    {#if loading}
      <div class="rounded-2xl border border-dashed border-slate-300 bg-white px-4 py-8 text-center text-slate-500">Loading connection profiles...</div>
    {:else}
      <div class="grid gap-6 xl:grid-cols-[1.1fr_0.9fr]">
        <div class="space-y-6">
          <section class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
            <div class="mb-4 flex items-center justify-between gap-3">
              <div>
                <h2 class="text-sm font-semibold uppercase tracking-wide text-slate-500">Built-in Profiles</h2>
                <p class="mt-1 text-sm text-slate-500">Seeded profiles optimized for common SSH workloads.</p>
              </div>
              <span class="rounded-full bg-slate-100 px-3 py-1 text-xs font-medium text-slate-600">Read only</span>
            </div>

            <div class="space-y-3">
              {#each builtinProfiles as profile}
                <button
                  type="button"
                  on:click={() => selectProfile(profile)}
                  class={`w-full rounded-xl border p-4 text-left transition ${selectedProfileId === profile.id ? 'border-blue-200 bg-blue-50/50' : 'border-slate-200 bg-slate-50 hover:border-slate-300 hover:bg-slate-100/70'}`}
                >
                  <div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
                    <div>
                      <div class="flex items-center gap-2">
                        <span class="font-medium text-slate-900">{profile.name}</span>
                        <span class="rounded-full bg-blue-100 px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide text-blue-700">Builtin</span>
                      </div>
                      {#if profile.description}
                        <p class="mt-1 text-sm text-slate-500">{profile.description}</p>
                      {/if}
                      <p class="mt-2 text-xs text-slate-500">{formatProfileSummary(profile)}</p>
                    </div>
                    <div class="text-xs text-slate-400">
                      {profile.backoffStrategy} · buffer {profile.bufferSizeKb} KB
                    </div>
                  </div>
                </button>
              {/each}
            </div>
          </section>

          <section class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
            <div class="mb-4 flex items-center justify-between gap-3">
              <div>
                <h2 class="text-sm font-semibold uppercase tracking-wide text-slate-500">Custom Profiles</h2>
                <p class="mt-1 text-sm text-slate-500">Create and manage profiles tailored to your environment.</p>
              </div>
              <span class="rounded-full bg-slate-100 px-3 py-1 text-xs font-medium text-slate-600">Editable</span>
            </div>

            {#if customProfiles.length === 0}
              <div class="rounded-xl border border-dashed border-slate-300 bg-slate-50 px-4 py-8 text-center text-slate-500">
                No custom profiles yet. Create one to override the built-in defaults.
              </div>
            {:else}
              <div class="space-y-3">
                {#each customProfiles as profile}
                  <div class={`rounded-xl border p-4 ${selectedProfileId === profile.id ? 'border-blue-200 bg-blue-50/50' : 'border-slate-200 bg-slate-50'}`}>
                    <div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
                      <button type="button" class="text-left" on:click={() => selectProfile(profile)}>
                        <div class="flex items-center gap-2">
                          <span class="font-medium text-slate-900">{profile.name}</span>
                          <span class="rounded-full bg-emerald-100 px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide text-emerald-700">Custom</span>
                        </div>
                        {#if profile.description}
                          <p class="mt-1 text-sm text-slate-500">{profile.description}</p>
                        {/if}
                        <p class="mt-2 text-xs text-slate-500">{formatProfileSummary(profile)}</p>
                      </button>

                      <div class="flex flex-wrap gap-2">
                        <button
                          type="button"
                          on:click={() => selectProfile(profile)}
                          class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50"
                        >
                          <PencilLine size={16} /> Edit
                        </button>
                        <button
                          type="button"
                          on:click={() => cloneSelected(profile)}
                          class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50"
                        >
                          <Copy size={16} /> Clone
                        </button>
                      </div>
                    </div>
                  </div>
                {/each}
              </div>
            {/if}
          </section>
        </div>

        <section class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
          <div class="mb-5 flex items-center justify-between gap-3">
            <div>
              <h2 class="text-sm font-semibold uppercase tracking-wide text-slate-500">
                {mode === 'new' ? 'Create Profile' : 'Profile Details'}
              </h2>
              <p class="mt-1 text-sm text-slate-500">
                {#if mode === 'view' && selectedProfile?.isBuiltin}
                  Built-in profiles are read-only. Clone one to make it editable.
                {:else if mode === 'edit'}
                  Update the selected custom profile.
                {:else}
                  Fill in the form to create a custom profile.
                {/if}
              </p>
            </div>
            {#if selectedProfile?.isBuiltin}
              <button
                type="button"
                on:click={() => cloneSelected(selectedProfile)}
                class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50"
              >
                <Copy size={16} /> Clone
              </button>
            {/if}
          </div>

          <div class="space-y-4">
            <div>
              <label class="mb-1 block text-sm font-medium text-slate-700">Name *</label>
              <input
                bind:value={name}
                disabled={mode === 'view'}
                class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20 disabled:bg-slate-50 disabled:text-slate-500"
                placeholder="Interactive"
              />
            </div>

            <div>
              <label class="mb-1 block text-sm font-medium text-slate-700">Description</label>
              <textarea
                bind:value={description}
                disabled={mode === 'view'}
                rows="3"
                class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20 disabled:bg-slate-50 disabled:text-slate-500"
                placeholder="Optimized for interactive shell sessions"
              ></textarea>
            </div>

            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <label class="mb-1 block text-sm font-medium text-slate-700">Timeout (s)</label>
                <input type="number" bind:value={timeoutSeconds} min="1" disabled={mode === 'view'} class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20 disabled:bg-slate-50 disabled:text-slate-500" />
              </div>
              <div>
                <label class="mb-1 block text-sm font-medium text-slate-700">Retry Count</label>
                <input type="number" bind:value={retryCount} min="0" disabled={mode === 'view'} class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20 disabled:bg-slate-50 disabled:text-slate-500" />
              </div>
              <div>
                <label class="mb-1 block text-sm font-medium text-slate-700">Retry Delay (ms)</label>
                <input type="number" bind:value={retryDelayMs} min="0" disabled={mode === 'view'} class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20 disabled:bg-slate-50 disabled:text-slate-500" />
              </div>
              <div>
                <label class="mb-1 block text-sm font-medium text-slate-700">Backoff Strategy</label>
                <select bind:value={backoffStrategy} disabled={mode === 'view'} class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20 disabled:bg-slate-50 disabled:text-slate-500">
                  <option value="exponential">Exponential</option>
                  <option value="linear">Linear</option>
                  <option value="fixed">Fixed</option>
                </select>
              </div>
              <div>
                <label class="mb-1 block text-sm font-medium text-slate-700">Keepalive (s)</label>
                <input type="number" bind:value={keepaliveSeconds} min="0" disabled={mode === 'view'} class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20 disabled:bg-slate-50 disabled:text-slate-500" />
              </div>
              <div>
                <label class="mb-1 block text-sm font-medium text-slate-700">Parallelism</label>
                <input type="number" bind:value={parallelism} min="1" disabled={mode === 'view'} class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20 disabled:bg-slate-50 disabled:text-slate-500" />
              </div>
              <div>
                <label class="mb-1 block text-sm font-medium text-slate-700">Buffer Size (KB)</label>
                <input type="number" bind:value={bufferSizeKb} min="1" disabled={mode === 'view'} class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20 disabled:bg-slate-50 disabled:text-slate-500" />
              </div>
              <div class="space-y-3 rounded-xl border border-slate-200 bg-slate-50 p-4">
                <label class="flex items-center gap-2 text-sm font-medium text-slate-700">
                  <input type="checkbox" bind:checked={reconnectEnabled} disabled={mode === 'view'} />
                  Reconnect enabled
                </label>
                <label class="flex items-center gap-2 text-sm font-medium text-slate-700">
                  <input type="checkbox" bind:checked={compression} disabled={mode === 'view'} />
                  Compression enabled
                </label>
              </div>
            </div>
          </div>

          <div class="mt-6 flex flex-wrap gap-3">
            <button
              type="button"
              on:click={saveProfile}
              disabled={mode === 'view' || saving}
              class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
            >
              <Layers3 size={16} />
              {saving ? 'Saving...' : mode === 'new' ? 'Create Profile' : 'Save Changes'}
            </button>

            {#if mode === 'edit'}
              <button
                type="button"
                on:click={deleteProfile}
                disabled={deleting}
                class="inline-flex items-center gap-2 rounded-lg border border-red-200 bg-white px-4 py-2 text-sm font-medium text-red-600 transition hover:bg-red-50 disabled:cursor-not-allowed disabled:opacity-50"
              >
                <Trash2 size={16} />
                {deleting ? 'Deleting...' : 'Delete Profile'}
              </button>
            {/if}
          </div>

          {#if selectedProfile && mode === 'view'}
            <div class="mt-6 rounded-xl border border-slate-200 bg-slate-50 p-4 text-sm text-slate-600">
              <p class="font-medium text-slate-900">{selectedProfile.name}</p>
              <p class="mt-2">{formatProfileSummary(selectedProfile)}</p>
              <p class="mt-2 text-xs text-slate-500">
                Built-in profiles cannot be edited directly. Use Clone to create a custom copy.
              </p>
            </div>
          {/if}
        </section>
      </div>
    {/if}
  </div>
</div>
