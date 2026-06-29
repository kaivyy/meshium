import { writable } from 'svelte/store';
import { api, setSessionToken, clearSessionToken } from '$lib/api/client';

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
  const res = await api.post<{ status: string; sessionToken: string }>('/auth/setup', { password });
  if (res?.sessionToken) {
    setSessionToken(res.sessionToken);
  }
  await checkStatus();
}

export async function unlock(password: string) {
  const res = await api.post<{ status: string; sessionToken: string }>('/auth/unlock', { password });
  if (res?.sessionToken) {
    setSessionToken(res.sessionToken);
  }
  await checkStatus();
}

export async function lock() {
  await api.post('/auth/lock');
  clearSessionToken();
  await checkStatus();
}
