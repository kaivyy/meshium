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

function installGlobals(token: string | null): { removed: string[] } {
  const removed: string[] = [];
  Object.defineProperty(globalThis, 'location', {
    configurable: true,
    value: { protocol: 'http:', host: 'meshium.test' },
  });
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => (key === 'meshium_session_token' ? token : null),
      removeItem: (key: string) => { removed.push(key); },
    },
  });
  Object.defineProperty(globalThis, 'WebSocket', {
    configurable: true,
    value: FakeWebSocket,
  });
  return { removed };
}

test('wsConnectGeneric connects once with the token subprotocol and never discards it', () => {
  FakeWebSocket.instances = [];
  const { removed } = installGlobals('valid-token');

  const received: unknown[] = [];
  let closed = false;
  const ws = wsConnectGeneric('/ws/terminal/1', (message) => received.push(message), undefined, () => {
    closed = true;
  });

  assert.equal(FakeWebSocket.instances.length, 1);
  assert.deepEqual(FakeWebSocket.instances[0].protocols, ['meshium-auth.valid-token']);

  // A transient close must NOT delete the token or open a second (tokenless) socket.
  FakeWebSocket.instances[0].fail();

  assert.deepEqual(removed, []);
  assert.equal(FakeWebSocket.instances.length, 1);
  assert.equal(closed, true);

  void ws;
});

test('wsConnectGeneric delivers messages from the single socket', () => {
  FakeWebSocket.instances = [];
  installGlobals('valid-token');

  const received: unknown[] = [];
  const ws = wsConnectGeneric('/ws/terminal/1', (message) => received.push(message));

  FakeWebSocket.instances[0].open();
  assert.equal(ws.readyState, FakeWebSocket.OPEN);

  ws.send('ping');
  assert.deepEqual(FakeWebSocket.instances[0].sent, ['ping']);

  FakeWebSocket.instances[0].message({ type: 'connected' });
  assert.deepEqual(received, [{ type: 'connected' }]);
});
