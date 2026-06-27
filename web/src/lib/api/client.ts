const BASE = '/api';

export class APIError extends Error {
  code: string;
  constructor(message: string, code: string) {
    super(message);
    this.code = code;
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : {},
    body: body ? JSON.stringify(body) : undefined
  });

  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: 'Unknown error', code: 'UNKNOWN' }));
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
