import { describe, it, expect } from 'vitest';
import type { PolicyMatrix } from '$lib/api/pipeline';
import {
  canAutoCutover,
  engineSupport,
  trafficProviderSupport,
  supportClasses
} from '$lib/support-status';

const POLICY: PolicyMatrix = {
  autoCutoverDefault: false,
  supportedEngines: ['postgres', 'mysql', 'redis'],
  supportedTrafficProviders: ['nginx', 'haproxy', 'caddy'],
  supportedReplicationModes: ['logical', 'physical'],
  supportedExecutionModes: ['online'],
  notes: ['mongodb: no safe automatic cutover contract', 'redis: degraded (no source freeze)']
};

describe('engineSupport', () => {
  it('reports automatic for a supported engine with no caveat', () => {
    const s = engineSupport('postgres', POLICY);
    expect(s.level).toBe('automatic');
    expect(s.reason).toMatch(/fenced/);
  });

  it('reports degraded for redis (no source-freeze) and never automatic', () => {
    const s = engineSupport('redis', POLICY);
    expect(s.level).toBe('degraded');
    expect(s.reason).toMatch(/minimal/i);
  });

  it('reports blocked for mongodb (no automatic contract)', () => {
    const s = engineSupport('mongodb', POLICY);
    expect(s.level).toBe('blocked');
  });

  it('reports deferred for an engine the server has never heard of', () => {
    const s = engineSupport('oracle', POLICY);
    expect(s.level).toBe('deferred');
  });

  it('degrades honestly when policy is not yet loaded (unknown, not automatic)', () => {
    const s = engineSupport('postgres', null);
    expect(s.level).toBe('unknown');
  });
});

describe('trafficProviderSupport', () => {
  it('reports automatic (fenced) for a supported provider', () => {
    const s = trafficProviderSupport('caddy', POLICY);
    expect(s.level).toBe('automatic');
    expect(s.reason).toMatch(/fenced switcher/);
  });

  it('reports manual-only for traefik (no fenced switcher)', () => {
    const s = trafficProviderSupport('traefik', POLICY);
    expect(s.level).toBe('manual');
  });

  it('is unknown when no provider selected', () => {
    const s = trafficProviderSupport(undefined, POLICY);
    expect(s.level).toBe('unknown');
  });
});

describe('canAutoCutover', () => {
  it('forbids automatic when no provider is chosen', () => {
    const r = canAutoCutover('postgres', undefined, POLICY);
    expect(r.allowed).toBe(false);
    expect(r.reason).toMatch(/fenced traffic provider/);
  });

  it('forbids automatic for a manual-only provider', () => {
    const r = canAutoCutover('postgres', 'traefik', POLICY);
    expect(r.allowed).toBe(false);
  });

  it('forbids automatic for a blocked engine even with a good provider', () => {
    const r = canAutoCutover('mongodb', 'nginx', POLICY);
    expect(r.allowed).toBe(false);
  });

  it('allows automatic for postgres + caddy (both supported)', () => {
    const r = canAutoCutover('postgres', 'caddy', POLICY);
    expect(r.allowed).toBe(true);
  });

  it('degrades to forbidden when policy is not loaded', () => {
    const r = canAutoCutover('postgres', 'caddy', null);
    expect(r.allowed).toBe(false);
    expect(r.reason).toMatch(/not loaded/);
  });
});

describe('supportClasses', () => {
  it('never returns an empty (color-only) class string', () => {
    for (const lvl of ['automatic', 'manual', 'degraded', 'blocked', 'deferred', 'staging-only', 'unknown'] as const) {
      expect(supportClasses(lvl).trim().length).toBeGreaterThan(0);
    }
  });
});
