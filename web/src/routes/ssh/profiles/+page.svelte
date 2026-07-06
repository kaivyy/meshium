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
        const created = await api.post('/connection-profiles', payload) as ConnectionProfile;
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
    <div class="rounded-2xl border border-border bg-surface p-6 shadow-sm">
      <div class="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <div class="inline-flex items-center gap-2 rounded-full bg-accent-subtle px-3 py-1 text-xs font-medium text-accent">
            <ServerCog size={14} /> SSH Profiles
          </div>
          <h1 class="mt-3 text-2xl font-bold tracking-tight text-fg">Connection Profiles</h1>
          <p class="mt-2 max-w-3xl text-sm text-fg-subtle">
            Configure timeouts, retry policies, keepalives, compression, and parallelism for SSH workflows.
          </p>
        </div>

        <div class="flex flex-wrap gap-3">
          <button
            type="button"
            on:click={() => startNewProfile()}
            class="inline-flex items-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition hover:bg-accent-hover"
          >
            <Plus size={16} /> New Profile
          </button>
          <a href="/ssh" class="inline-flex items-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted">
            Back to Dashboard
          </a>
        </div>
      </div>

      <div class="mt-6 grid gap-4 md:grid-cols-3">
        <div class="rounded-xl border border-border bg-surface-muted p-4">
          <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Built-in Profiles</p>
          <p class="mt-2 text-2xl font-bold text-fg">{builtinProfiles.length}</p>
        </div>
        <div class="rounded-xl border border-border bg-surface-muted p-4">
          <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Custom Profiles</p>
          <p class="mt-2 text-2xl font-bold text-fg">{customProfiles.length}</p>
        </div>
        <div class="rounded-xl border border-border bg-surface-muted p-4">
          <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Selected Mode</p>
          <p class="mt-2 text-2xl font-bold text-fg">{mode}</p>
        </div>
      </div>
    </div>

    {#if error}
      <div class="rounded-2xl border border-error bg-error/10 px-4 py-3 text-sm text-error" role="alert">{error}</div>
    {/if}

    {#if loading}
      <div class="rounded-2xl border border-dashed border-border-strong bg-surface px-4 py-8 text-center text-fg-subtle">Loading connection profiles...</div>
    {:else}
      <div class="grid gap-6 xl:grid-cols-[1.1fr_0.9fr]">
        <div class="space-y-6">
          <section class="rounded-2xl border border-border bg-surface p-5 shadow-sm">
            <div class="mb-4 flex items-center justify-between gap-3">
              <div>
                <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">Built-in Profiles</h2>
                <p class="mt-1 text-sm text-fg-subtle">Seeded profiles optimized for common SSH workloads.</p>
              </div>
              <span class="rounded-full bg-surface-muted px-3 py-1 text-xs font-medium text-fg-muted">Read only</span>
            </div>

            <div class="space-y-3">
              {#each builtinProfiles as profile}
                <button
                  type="button"
                  on:click={() => selectProfile(profile)}
                  class={`w-full rounded-xl border p-4 text-left transition ${selectedProfileId === profile.id ? 'border-accent bg-accent-subtle/50' : 'border-border bg-surface-muted hover:border-border-strong hover:bg-surface-muted/70'}`}
                >
                  <div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
                    <div>
                      <div class="flex items-center gap-2">
                        <span class="font-medium text-fg">{profile.name}</span>
                        <span class="rounded-full bg-accent-subtle px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide text-accent">Builtin</span>
                      </div>
                      {#if profile.description}
                        <p class="mt-1 text-sm text-fg-subtle">{profile.description}</p>
                      {/if}
                      <p class="mt-2 text-xs text-fg-subtle">{formatProfileSummary(profile)}</p>
                    </div>
                    <div class="text-xs text-fg-subtle">
                      {profile.backoffStrategy} · buffer {profile.bufferSizeKb} KB
                    </div>
                  </div>
                </button>
              {/each}
            </div>
          </section>

          <section class="rounded-2xl border border-border bg-surface p-5 shadow-sm">
            <div class="mb-4 flex items-center justify-between gap-3">
              <div>
                <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">Custom Profiles</h2>
                <p class="mt-1 text-sm text-fg-subtle">Create and manage profiles tailored to your environment.</p>
              </div>
              <span class="rounded-full bg-surface-muted px-3 py-1 text-xs font-medium text-fg-muted">Editable</span>
            </div>

            {#if customProfiles.length === 0}
              <div class="rounded-xl border border-dashed border-border-strong bg-surface-muted px-4 py-8 text-center text-fg-subtle">
                No custom profiles yet. Create one to override the built-in defaults.
              </div>
            {:else}
              <div class="space-y-3">
                {#each customProfiles as profile}
                  <div class={`rounded-xl border p-4 ${selectedProfileId === profile.id ? 'border-accent bg-accent-subtle/50' : 'border-border bg-surface-muted'}`}>
                    <div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
                      <button type="button" class="text-left" on:click={() => selectProfile(profile)}>
                        <div class="flex items-center gap-2">
                          <span class="font-medium text-fg">{profile.name}</span>
                          <span class="rounded-full bg-success/15 px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide text-success">Custom</span>
                        </div>
                        {#if profile.description}
                          <p class="mt-1 text-sm text-fg-subtle">{profile.description}</p>
                        {/if}
                        <p class="mt-2 text-xs text-fg-subtle">{formatProfileSummary(profile)}</p>
                      </button>

                      <div class="flex flex-wrap gap-2">
                        <button
                          type="button"
                          on:click={() => selectProfile(profile)}
                          class="inline-flex items-center gap-2 rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted"
                        >
                          <PencilLine size={16} /> Edit
                        </button>
                        <button
                          type="button"
                          on:click={() => cloneSelected(profile)}
                          class="inline-flex items-center gap-2 rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted"
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

        <section class="rounded-2xl border border-border bg-surface p-5 shadow-sm">
          <div class="mb-5 flex items-center justify-between gap-3">
            <div>
              <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">
                {mode === 'new' ? 'Create Profile' : 'Profile Details'}
              </h2>
              <p class="mt-1 text-sm text-fg-subtle">
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
                class="inline-flex items-center gap-2 rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted"
              >
                <Copy size={16} /> Clone
              </button>
            {/if}
          </div>

          <div class="space-y-4">
            <div>
              <label for="profile-name" class="mb-1 block text-sm font-medium text-fg-muted">Name *</label>
              <input
                id="profile-name"
                bind:value={name}
                disabled={mode === 'view'}
                class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20 disabled:bg-surface-muted disabled:text-fg-subtle"
                placeholder="Interactive"
              />
            </div>

            <div>
              <label for="profile-description" class="mb-1 block text-sm font-medium text-fg-muted">Description</label>
              <textarea
                id="profile-description"
                bind:value={description}
                disabled={mode === 'view'}
                rows="3"
                class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20 disabled:bg-surface-muted disabled:text-fg-subtle"
                placeholder="Optimized for interactive shell sessions"
              ></textarea>
            </div>

            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <label for="profile-timeout" class="mb-1 block text-sm font-medium text-fg-muted">Timeout (s)</label>
                <input id="profile-timeout" type="number" bind:value={timeoutSeconds} min="1" disabled={mode === 'view'} class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20 disabled:bg-surface-muted disabled:text-fg-subtle" />
              </div>
              <div>
                <label for="profile-retry-count" class="mb-1 block text-sm font-medium text-fg-muted">Retry Count</label>
                <input id="profile-retry-count" type="number" bind:value={retryCount} min="0" disabled={mode === 'view'} class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20 disabled:bg-surface-muted disabled:text-fg-subtle" />
              </div>
              <div>
                <label for="profile-retry-delay" class="mb-1 block text-sm font-medium text-fg-muted">Retry Delay (ms)</label>
                <input id="profile-retry-delay" type="number" bind:value={retryDelayMs} min="0" disabled={mode === 'view'} class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20 disabled:bg-surface-muted disabled:text-fg-subtle" />
              </div>
              <div>
                <label for="profile-backoff-strategy" class="mb-1 block text-sm font-medium text-fg-muted">Backoff Strategy</label>
                <select id="profile-backoff-strategy" bind:value={backoffStrategy} disabled={mode === 'view'} class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20 disabled:bg-surface-muted disabled:text-fg-subtle">
                  <option value="exponential">Exponential</option>
                  <option value="linear">Linear</option>
                  <option value="fixed">Fixed</option>
                </select>
              </div>
              <div>
                <label for="profile-keepalive" class="mb-1 block text-sm font-medium text-fg-muted">Keepalive (s)</label>
                <input id="profile-keepalive" type="number" bind:value={keepaliveSeconds} min="0" disabled={mode === 'view'} class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20 disabled:bg-surface-muted disabled:text-fg-subtle" />
              </div>
              <div>
                <label for="profile-parallelism" class="mb-1 block text-sm font-medium text-fg-muted">Parallelism</label>
                <input id="profile-parallelism" type="number" bind:value={parallelism} min="1" disabled={mode === 'view'} class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20 disabled:bg-surface-muted disabled:text-fg-subtle" />
              </div>
              <div>
                <label for="profile-buffer-size" class="mb-1 block text-sm font-medium text-fg-muted">Buffer Size (KB)</label>
                <input id="profile-buffer-size" type="number" bind:value={bufferSizeKb} min="1" disabled={mode === 'view'} class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20 disabled:bg-surface-muted disabled:text-fg-subtle" />
              </div>
              <div class="space-y-3 rounded-xl border border-border bg-surface-muted p-4">
                <label class="flex items-center gap-2 text-sm font-medium text-fg-muted">
                  <input type="checkbox" bind:checked={reconnectEnabled} disabled={mode === 'view'} />
                  Reconnect enabled
                </label>
                <label class="flex items-center gap-2 text-sm font-medium text-fg-muted">
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
              class="inline-flex items-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-50"
            >
              <Layers3 size={16} />
              {saving ? 'Saving...' : mode === 'new' ? 'Create Profile' : 'Save Changes'}
            </button>

            {#if mode === 'edit'}
              <button
                type="button"
                on:click={deleteProfile}
                disabled={deleting}
                class="inline-flex items-center gap-2 rounded-lg border border-error bg-surface px-4 py-2 text-sm font-medium text-error transition hover:bg-error/10 disabled:cursor-not-allowed disabled:opacity-50"
              >
                <Trash2 size={16} />
                {deleting ? 'Deleting...' : 'Delete Profile'}
              </button>
            {/if}
          </div>

          {#if selectedProfile && mode === 'view'}
            <div class="mt-6 rounded-xl border border-border bg-surface-muted p-4 text-sm text-fg-muted">
              <p class="font-medium text-fg">{selectedProfile.name}</p>
              <p class="mt-2">{formatProfileSummary(selectedProfile)}</p>
              <p class="mt-2 text-xs text-fg-subtle">
                Built-in profiles cannot be edited directly. Use Clone to create a custom copy.
              </p>
            </div>
          {/if}
        </section>
      </div>
    {/if}
  </div>
</div>
