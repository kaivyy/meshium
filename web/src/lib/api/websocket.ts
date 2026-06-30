export function wsConnect(
  serverId: number,
  onMessage: (msg: WSMessage) => void,
  onError?: (err: Event) => void,
  onClose?: () => void
): WebSocket {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const token = typeof localStorage !== 'undefined' ? localStorage.getItem('meshium_session_token') : null;
  const tokenParam = token ? `?token=${encodeURIComponent(token)}` : '';
  const url = `${proto}://${location.host}/ws/connect/${serverId}${tokenParam}`;
  const ws = new WebSocket(url);

  ws.onmessage = (e) => {
    const msg = JSON.parse(e.data) as WSMessage;
    onMessage(msg);
  };

  ws.onerror = (e) => onError?.(e);
  ws.onclose = () => onClose?.();

  return ws;
}

export interface WSMessage {
  step: string;
  status: 'success' | 'error' | 'complete';
  value?: unknown;
  error?: string;
  latencyMs?: number;
}
