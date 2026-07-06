import { writable } from 'svelte/store';
import { browser } from '$app/environment';

export type ThemeMode = 'light' | 'dark' | 'system';

const STORAGE_KEY = 'meshium-theme';

function readStored(): ThemeMode {
  if (!browser) return 'system';
  const v = localStorage.getItem(STORAGE_KEY);
  return v === 'light' || v === 'dark' || v === 'system' ? v : 'system';
}

function systemPrefersDark(): boolean {
  return browser && window.matchMedia('(prefers-color-scheme: dark)').matches;
}

function resolveDark(mode: ThemeMode): boolean {
  return mode === 'dark' || (mode === 'system' && systemPrefersDark());
}

function applyClass(mode: ThemeMode): void {
  if (!browser) return;
  document.documentElement.classList.toggle('dark', resolveDark(mode));
}

export const themeStore = writable<ThemeMode>(readStored());

export function setTheme(mode: ThemeMode): void {
  if (browser) localStorage.setItem(STORAGE_KEY, mode);
  applyClass(mode);
  themeStore.set(mode);
}

const ORDER: ThemeMode[] = ['light', 'dark', 'system'];
export function cycleTheme(current: ThemeMode): void {
  const next = ORDER[(ORDER.indexOf(current) + 1) % ORDER.length];
  setTheme(next);
}

export function initTheme(): void {
  if (!browser) return;
  const mode = readStored();
  applyClass(mode);
  const mq = window.matchMedia('(prefers-color-scheme: dark)');
  mq.addEventListener('change', () => {
    if (readStored() === 'system') applyClass('system');
  });
}
