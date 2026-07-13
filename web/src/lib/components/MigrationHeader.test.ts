import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import MigrationHeader from './MigrationHeader.svelte';
import type { RiskReport, StrategySelection } from '$lib/api/pipeline';

const base = {
  migrationId: 1,
  currentState: 'running',
  sourceId: 1,
  targetId: 2,
  healthScore: 90,
  riskReport: null as RiskReport | null,
  strategy: null as StrategySelection | null,
  rollbackAvailable: true,
  pipelineRunning: true,
  pipelinePaused: false,
  wsConnectionState: 'connected' as const,
  autoCutoverConfigured: false,
};

describe('MigrationHeader WS indicator (3E)', () => {
  it('shows Live when connected', () => {
    const el = render(MigrationHeader, { ...base, wsConnectionState: 'connected' });
    expect(el.getByText(/Live/i)).toBeTruthy();
    expect(el.queryByText(/Stale/i)).toBeNull();
  });

  it('never shows Live while stale — warns data may be out of date', () => {
    const el = render(MigrationHeader, { ...base, wsConnectionState: 'stale' });
    expect(el.queryByText(/Live/i)).toBeNull();
    expect(el.getByText(/Stale/i)).toBeTruthy();
  });

  it('shows Replaying history during reconnect replay', () => {
    const el = render(MigrationHeader, { ...base, wsConnectionState: 'replaying' });
    expect(el.getByText(/Replaying history/i)).toBeTruthy();
    expect(el.queryByText(/Live/i)).toBeNull();
  });

  it('awaiting_cutover copy is manual when autoCutover is off, automatic when on', () => {
    const manual = render(MigrationHeader, { ...base, currentState: 'awaiting_cutover', autoCutoverConfigured: false });
    expect(manual.getByTitle(/Manual cutover required/i)).toBeTruthy();
    const auto = render(MigrationHeader, { ...base, currentState: 'awaiting_cutover', autoCutoverConfigured: true });
    expect(auto.getByTitle(/Automatic cutover is configured/i)).toBeTruthy();
  });
});
