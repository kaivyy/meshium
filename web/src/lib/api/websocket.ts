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
  const ws = subprotocols.length > 0
    ? new WebSocket(url, subprotocols)
    : new WebSocket(url);

  ws.onopen = () => onOpen?.();
  ws.onmessage = (e) => {
    try {
      const msg = JSON.parse(e.data) as T;
      onMessage(msg);
    } catch {
      // Ignore non-JSON messages
    }
  };

  ws.onerror = (e) => onError?.(e);
  ws.onclose = () => onClose?.();

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
