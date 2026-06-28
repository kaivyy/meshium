const BASE = '/api';

export class APIError extends Error {
  code: string;
  constructor(message: string, code: string) {
    super(message);
    this.code = code;
  }
}

function getSessionToken(): string | null {
  if (typeof localStorage === 'undefined') return null;
  return localStorage.getItem('meshium_session_token');
}

export function setSessionToken(token: string) {
  if (typeof localStorage === 'undefined') return;
  localStorage.setItem('meshium_session_token', token);
}

export function clearSessionToken() {
  if (typeof localStorage === 'undefined') return;
  localStorage.removeItem('meshium_session_token');
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const token = getSessionToken();
  const headers: Record<string, string> = {};
  if (body) {
    headers['Content-Type'] = 'application/json';
  }
  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }

  const res = await fetch(`${BASE}${path}`, {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined
  });

  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: 'Unknown error', code: 'UNKNOWN' }));
    // On 401 or 403 (locked), clear the stale session token and redirect to login
    if (res.status === 401 || (res.status === 403 && err.code === 'LOCKED')) {
      clearSessionToken();
      if (typeof window !== 'undefined' && !window.location.pathname.startsWith('/login') && !window.location.pathname.startsWith('/setup')) {
        window.location.href = '/login';
      }
    }
    throw new APIError(err.error, err.code);
  }

  const contentLength = res.headers.get('content-length');
  const contentType = res.headers.get('content-type')?.toLowerCase();

  if (res.status === 204 || contentLength === '0') {
    return undefined as T;
  }

  if (!contentType || !contentType.includes('application/json')) {
    return undefined as T;
  }

  return res.json();
}

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body),
  put: <T>(path: string, body?: unknown) => request<T>('PUT', path, body),
  delete: <T>(path: string) => request<T>('DELETE', path),
  patch: <T>(path: string, body?: unknown) => request<T>('PATCH', path, body)
};
