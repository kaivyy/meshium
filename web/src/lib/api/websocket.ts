export function wsConnect(
  serverId: number,
  onMessage: (msg: WSMessage) => void,
  onError?: (err: Event) => void,
  onClose?: () => void
): WebSocket {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const url = `${proto}://${location.host}/ws/connect/${serverId}`;
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
