import assert from 'node:assert/strict';
import { test } from 'node:test';

import { wsConnectGeneric } from './websocket';

type Handler<T = Event> = ((event: T) => void) | null;

class FakeWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;
  static instances: FakeWebSocket[] = [];

  readonly url: string;
  readonly protocols: string[];
  readyState = FakeWebSocket.CONNECTING;
  sent: string[] = [];

  onopen: Handler = null;
  onmessage: Handler<MessageEvent> = null;
  onerror: Handler = null;
  onclose: Handler = null;

  constructor(url: string, protocols?: string | string[]) {
    this.url = url;
    this.protocols = Array.isArray(protocols) ? protocols : protocols ? [protocols] : [];
    FakeWebSocket.instances.push(this);
  }

  open() {
    this.readyState = FakeWebSocket.OPEN;
    this.onopen?.(new Event('open'));
  }

  fail() {
    this.readyState = FakeWebSocket.CLOSED;
    this.onerror?.(new Event('error'));
    this.onclose?.(new Event('close'));
  }

  message(data: unknown) {
    this.onmessage?.({ data: JSON.stringify(data) } as MessageEvent);
  }

  send(data: string) {
    this.sent.push(data);
  }

  close() {
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.(new Event('close'));
  }
}

test('wsConnectGeneric retries stale subprotocol auth and delegates to the retry socket', () => {
  FakeWebSocket.instances = [];
  let removedToken = '';

  Object.defineProperty(globalThis, 'location', {
    configurable: true,
    value: { protocol: 'http:', host: 'meshium.test' },
  });
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => key === 'meshium_session_token' ? 'stale-token' : null,
      removeItem: (key: string) => { removedToken = key; },
    },
  });
  Object.defineProperty(globalThis, 'WebSocket', {
    configurable: true,
    value: FakeWebSocket,
  });

  const received: unknown[] = [];
  const ws = wsConnectGeneric('/ws/terminal/1', (message) => received.push(message));

  assert.equal(FakeWebSocket.instances.length, 1);
  assert.deepEqual(FakeWebSocket.instances[0].protocols, ['meshium-auth.stale-token']);

  FakeWebSocket.instances[0].fail();

  assert.equal(removedToken, 'meshium_session_token');
  assert.equal(FakeWebSocket.instances.length, 2);
  assert.deepEqual(FakeWebSocket.instances[1].protocols, []);

  FakeWebSocket.instances[1].open();
  assert.equal(ws.readyState, FakeWebSocket.OPEN);

  ws.send('ping');
  assert.deepEqual(FakeWebSocket.instances[1].sent, ['ping']);

  FakeWebSocket.instances[1].message({ type: 'connected' });
  assert.deepEqual(received, [{ type: 'connected' }]);
});
