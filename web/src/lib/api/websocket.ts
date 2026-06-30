// Generic WebSocket client for Meshium.
// All WebSocket connections use session token authentication via query parameter.

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
 * Build a WebSocket URL with auth token for the given path.
 * The path should start with / (e.g., /ws/connect/1, /ws/terminal/1).
 */
export function wsURL(path: string): string {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const token = getSessionToken();
  const tokenParam = token ? `?token=${encodeURIComponent(token)}` : '';
  return `${proto}://${location.host}${path}${tokenParam}`;
}

/**
 * Connect to a WebSocket endpoint with automatic token injection.
 * Returns the WebSocket instance for the caller to manage.
 *
 * @param path - WebSocket path starting with / (e.g., /ws/terminal/1)
 * @param onMessage - Callback for each message (parsed as JSON)
 * @param onError - Optional error callback
 * @param onClose - Optional close callback
 */
export function wsConnectGeneric(
  path: string,
  onMessage: (msg: WSMessage) => void,
  onError?: (err: Event) => void,
  onClose?: () => void
): WebSocket {
  const url = wsURL(path);
  const ws = new WebSocket(url);

  ws.onmessage = (e) => {
    try {
      const msg = JSON.parse(e.data) as WSMessage;
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
