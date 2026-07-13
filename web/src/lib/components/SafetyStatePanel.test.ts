import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import SafetyStatePanel from './SafetyStatePanel.svelte';

describe('SafetyStatePanel', () => {
  const base = {
    currentState: '',
    healthScore: 100,
    replicationLag: 0,
    rollbackAvailable: false,
    observationRemaining: 0,
    runbookHref: '#'
  };

  it('renders nothing for a non-safety state (self-guarded)', () => {
    render(SafetyStatePanel, { ...base, currentState: 'running' });
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('shows awaiting_cutover with manual copy when autoCutover is off', () => {
    render(SafetyStatePanel, { ...base, currentState: 'awaiting_cutover', autoCutoverConfigured: false });
    expect(screen.getByRole('alert')).toBeTruthy();
    expect(screen.getByText(/Manual cutover required/i)).toBeTruthy();
    expect(screen.queryByText(/Automatic \(fenced\) cutover is configured/i)).toBeNull();
  });

  it('shows automatic copy when autoCutover is configured', () => {
    render(SafetyStatePanel, { ...base, currentState: 'awaiting_cutover', autoCutoverConfigured: true });
    expect(screen.getByText(/Automatic \(fenced\) cutover is configured/i)).toBeTruthy();
  });

  it('needs_manual_intervention forbids assumptions and links runbook', () => {
    const el = render(SafetyStatePanel, { ...base, currentState: 'needs_manual_intervention', rollbackAvailable: true });
    expect(el.getByText(/Needs manual intervention/i)).toBeTruthy();
    expect(el.getByText(/do not assume the source is still the writer/i)).toBeTruthy();
    expect(el.getByText(/Open runbook/i)).toBeTruthy();
  });

  it('rollback_degraded warns against auto-rollback after target writes', () => {
    const el = render(SafetyStatePanel, { ...base, currentState: 'rollback_degraded' });
    expect(el.getByText(/Rollback degraded/i)).toBeTruthy();
    expect(el.getByText(/do not auto-rollback again after target writes/i)).toBeTruthy();
  });

  it('observing states the rollback-after-target-write caveat', () => {
    const el = render(SafetyStatePanel, { ...base, currentState: 'post_verification_observation', observationRemaining: 120 });
    expect(el.getByText(/Observing/i)).toBeTruthy();
    expect(el.getByText(/≈ 120s remaining/i)).toBeTruthy();
    expect(el.getByText(/Do not assume rollback is safe after target writes/i)).toBeTruthy();
  });
});
