import { describe, it, expect } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import CutoverChecklist from './CutoverChecklist.svelte';
import ObservationPanel from './ObservationPanel.svelte';

const baseCutover = {
  replicationLag: 2,
  healthScore: 90,
  replicationStatus: [],
  queueStates: [],
  containerHealth: [],
  rollbackAvailable: true,
  actionLoading: false,
  onCutover: () => {},
};

const baseObservation = {
  healthScore: 90,
  replicationLag: 1,
  observationElapsed: 0,
  observationDuration: 60,
  observationRemaining: 60,
  observationProgress: 100,
  stepStatus: 'completed',
  actionLoading: false,
  onCommit: () => {},
  onRollback: () => {},
};

describe('CutoverChecklist action safety', () => {
  it('disables Start Cutover when live data is stale and explains why', async () => {
    render(CutoverChecklist, { props: { ...baseCutover, staleData: true } });
    const btn = screen.getByRole('button', { name: /Start Cutover/i }) as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    expect(btn.title).toMatch(/wait for reconnection/i);
    expect(screen.getByText(/Live data unavailable/i)).toBeTruthy();
  });

  it('shows checks-remaining copy and disables when a check fails', async () => {
    render(CutoverChecklist, { props: { ...baseCutover, healthScore: 50 } });
    const btn = screen.getByRole('button', { name: /Start Cutover/i }) as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    expect(screen.getByText(/\d+ checks remaining/i)).toBeTruthy();
  });

  it('fires onCutover only when enabled', async () => {
    let fired = 0;
    render(CutoverChecklist, { props: { ...baseCutover, replicationStatus: [{ status: 'running' }], containerHealth: [{ name: 'worker', healthy: true }], onCutover: () => fired++ } });
    const btn = screen.getByRole('button', { name: /Start Cutover/i }) as HTMLButtonElement;
    expect(btn.disabled).toBe(false);
    await fireEvent.click(btn);
    expect(fired).toBe(1);
  });
});

describe('ObservationPanel action safety', () => {
  it('disables Commit and Rollback while live data is stale', async () => {
    render(ObservationPanel, { props: { ...baseObservation, staleData: true } });
    const commit = screen.getByRole('button', { name: /Commit Migration/i }) as HTMLButtonElement;
    const rollback = screen.getByRole('button', { name: /Rollback/i }) as HTMLButtonElement;
    expect(commit.disabled).toBe(true);
    expect(rollback.disabled).toBe(true);
    expect(commit.title).toMatch(/wait for reconnection before committing/i);
  });

  it('warns that rollback is no longer safe after commit', async () => {
    render(ObservationPanel, { props: { ...baseObservation } });
    // commit button tooltip states the irreversible caveat
    const commit = screen.getByRole('button', { name: /Commit Migration/i }) as HTMLButtonElement;
    expect(commit.title).toMatch(/rollback is no longer safe/i);
  });
});
