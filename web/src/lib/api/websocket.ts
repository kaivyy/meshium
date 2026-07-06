// Generic WebSocket client for Meshium.
// WebSocket connections use subprotocol-based authentication (Sec-WebSocket-Protocol header)
// to avoid exposing the session token in the URL (which would appear in logs, browser history, etc.).
// The token is sent as: Sec-WebSocket-Protocol: meshium-auth.<token>
// A legacy fallback to query parameter is kept for backward compatibility.

export interface WSMessage {
  step: string;
  status: 'success' | 'error' | 'complete';
  value?: unknown;
  error?: string;
  latencyMs?: number;
}

function getSessionToken(): string | null {
  if (typeof localStorage === 'undefined') return null;
  return localStorage.getItem('meshium_session_token');
}

/**
 * Build a WebSocket URL for the given path (without token in the URL).
 * The path should start with / (e.g., /ws/connect/1, /ws/terminal/1).
 */
export function wsURL(path: string): string {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const url = new URL(path, `${proto}://${location.host}`);
  return url.toString();
}

/**
 * Build the subprotocol array for WebSocket authentication.
 * The token is sent as a subprotocol: "meshium-auth.<token>"
 * This avoids exposing the token in the URL.
 */
function wsSubprotocols(): string[] {
  const token = getSessionToken();
  if (token) {
    return [`meshium-auth.${token}`];
  }
  return [];
}

/**
 * Connect to a WebSocket endpoint with automatic token injection via subprotocol.
 * Returns the WebSocket instance for the caller to manage.
 *
 * @param path - WebSocket path starting with / (e.g., /ws/terminal/1)
 * @param onMessage - Callback for each message (parsed as JSON)
 * @param onError - Optional error callback
 * @param onClose - Optional close callback
 */
export function wsConnectGeneric<T = WSMessage>(
  path: string,
  onMessage: (msg: T) => void,
  onError?: (err: Event) => void,
  onClose?: () => void,
  onOpen?: () => void
): WebSocket {
  const url = wsURL(path);
  const subprotocols = wsSubprotocols();
  let gotMessage = false;
  let retried = false;
  let closed = false;
  let activeSocket: WebSocket;

  const ws = new Proxy({} as WebSocket, {
    get(_target, prop) {
      if (prop === 'close') {
        return (code?: number, reason?: string) => {
          closed = true;
          activeSocket.close(code, reason);
        };
      }

      const value = (activeSocket as any)[prop];
      return typeof value === 'function' ? value.bind(activeSocket) : value;
    },
    set(_target, prop, value) {
      (activeSocket as any)[prop] = value;
      return true;
    }
  });

  function bindSocket(socket: WebSocket, isRetry = false) {
    activeSocket = socket;

    socket.onopen = () => onOpen?.();
    socket.onmessage = (e) => {
      gotMessage = true;
      try {
        const msg = JSON.parse(e.data) as T;
        onMessage(msg);
      } catch {
        // Ignore non-JSON messages
      }
    };

    // Suppress error during first attempt if we have a token to retry without
    socket.onerror = (e) => {
      if (!isRetry && !retried && subprotocols.length > 0) return;
      onError?.(e);
    };

    socket.onclose = () => {
      if (closed) {
        onClose?.();
        return;
      }

      if (!gotMessage && !retried && subprotocols.length > 0) {
        // First attempt failed with stale token — retry without token
        retried = true;
        if (typeof localStorage !== 'undefined') {
          localStorage.removeItem('meshium_session_token');
        }
        bindSocket(new WebSocket(url), true);
        return;
      }

      onClose?.();
    };
  }

  bindSocket(subprotocols.length > 0
    ? new WebSocket(url, subprotocols)
    : new WebSocket(url));

  return ws;
}

/**
 * Connect to the discovery WebSocket endpoint for a server.
 * This is a convenience wrapper around wsConnectGeneric for backward compatibility.
 */
export function wsConnect(
  serverId: number,
  onMessage: (msg: WSMessage) => void,
  onError?: (err: Event) => void,
  onClose?: () => void
): WebSocket {
  return wsConnectGeneric(`/ws/connect/${serverId}`, onMessage, onError, onClose);
}
