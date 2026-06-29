import { writable } from 'svelte/store';

export type ToastVariant = 'success' | 'error' | 'warning' | 'info';

export interface Toast {
  id: string;
  variant: ToastVariant;
  message: string;
  duration: number;
}

const MAX_TOASTS = 5;
const DEFAULT_DURATION = 4000;

export const toasts = writable<Toast[]>([]);

function genId(): string {
  return `toast-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

export function pushToast(message: string, variant: ToastVariant = 'info', duration: number = DEFAULT_DURATION): void {
  const toast: Toast = { id: genId(), variant, message, duration };
  toasts.update((list) => {
    const next = [...list, toast];
    return next.slice(-MAX_TOASTS);
  });
  setTimeout(() => dismissToast(toast.id), duration);
}

export function dismissToast(id: string): void {
  toasts.update((list) => list.filter((t) => t.id !== id));
}

export const toast = {
  success: (msg: string) => pushToast(msg, 'success'),
  error: (msg: string) => pushToast(msg, 'error'),
  warning: (msg: string) => pushToast(msg, 'warning'),
  info: (msg: string) => pushToast(msg, 'info'),
};
