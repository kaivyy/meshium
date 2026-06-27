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
  loading: true
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
