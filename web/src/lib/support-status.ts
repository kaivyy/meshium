import type { PolicyMatrix } from '$lib/api/pipeline';

// Canonical UI support-status model (Phase 4G, slice 3A).
//
// The backend is the source of truth. This module ONLY reflects the server's
// PolicyMatrix and the migration's own state — it never re-derives a safety or
// authorization decision. If the policy is unknown (not yet fetched), every
// helper degrades conservatively and honestly rather than asserting a stronger
// capability than evidence supports.

export type SupportLevel =
  | 'automatic'
  | 'manual'
  | 'degraded'
  | 'blocked'
  | 'deferred'
  | 'staging-only'
  | 'unknown';

export interface SupportStatus {
  level: SupportLevel;
  // Human-readable label, e.g. "Automatic (fenced)", "Manual cutover", "Degraded".
  label: string;
  // Why this level applies — quoted from policy notes when available.
  reason: string;
  // What the operator must do to reach a stronger level (empty if already max).
  upgrade: string;
  // What the operator can safely do next.
  nextAction: string;
}

const ENGINE_DEGRADED = new Set(['redis']); // no source-freeze primitive
const ENGINE_BLOCKED_AUTOMATIC = new Set(['mongodb']); // no safe automatic contract

export function trafficProviderSupport(
  provider: string | undefined,
  policy: PolicyMatrix | null
): SupportStatus {
  if (!provider) {
    return {
      level: 'unknown',
      label: 'No provider selected',
      reason: 'Select a traffic provider to see its support status.',
      upgrade: 'Choose a provider below.',
      nextAction: 'Select a traffic provider.'
    };
  }
  const supported = policy?.supportedTrafficProviders ?? [];
  if (supported.includes(provider)) {
    return {
      level: 'automatic',
      label: 'Automatic (fenced)',
      reason: `${provider} has a fenced switcher with read-after-write ownership proof.`,
      upgrade: '',
      nextAction: 'Enable automatic cutover if the engine also supports it.'
    };
  }
  return {
    level: 'manual',
    label: 'Manual only',
    reason: `${provider} has no fenced switcher — selectable only as a manual switch, never automatic.`,
    upgrade: 'Use nginx, haproxy, or caddy for an automatic (fenced) cutover.',
    nextAction: 'Plan a manual cutover; confirm traffic move yourself at commit.'
  };
}

export function engineSupport(
  engine: string | undefined,
  policy: PolicyMatrix | null
): SupportStatus {
  if (!engine) {
    return {
      level: 'unknown',
      label: 'Engine unknown',
      reason: 'No engine detected yet.',
      upgrade: '',
      nextAction: 'Detect the engine during planning.'
    };
  }
  const supported = new Set(policy?.supportedEngines ?? []);
  if (!policy) {
    return {
      level: 'unknown',
      label: 'Unknown (policy not loaded)',
      reason: 'Support policy has not loaded yet — cannot assess this engine.',
      upgrade: 'Wait for the policy to load, then re-check.',
      nextAction: 'Re-select the engine after the support policy loads.'
    };
  }
  if (supported.has(engine)) {
    if (ENGINE_DEGRADED.has(engine)) {
      return {
        level: 'degraded',
        label: 'Degraded',
        reason: `${engine} has no source-freeze primitive — minimal (not zero) downtime; never automatic.`,
        upgrade: 'Freeze source writes manually before cutover.',
        nextAction: 'Plan a manual cutover with a maintenance window.'
      };
    }
    return {
      level: 'automatic',
      label: 'Automatic (fenced)',
      reason: `${engine} supports a fenced automatic cutover on supported providers.`,
      upgrade: '',
      nextAction: 'Enable automatic cutover if the provider also supports it.'
    };
  }
  if (ENGINE_BLOCKED_AUTOMATIC.has(engine)) {
    return {
      level: 'blocked',
      label: 'Blocked (automatic)',
      reason: `${engine} has no safe automatic cutover contract (no replica-set lag measurement).`,
      upgrade: 'Automatic cutover is not available; use the manual path.',
      nextAction: 'Plan a manual cutover; the operator performs step-down.'
    };
  }
  return {
    level: 'deferred',
    label: 'Deferred',
    reason: `${engine} is not yet proven for cutover.`,
    upgrade: 'Not supported in this release.',
    nextAction: 'Use the manual path only if the engine permits it.'
  };
}

// canAutoCutover answers whether the current selection is permitted by policy.
// It does NOT authorize — the backend still enforces at the configure boundary.
export function canAutoCutover(
  engine: string | undefined,
  provider: string | undefined,
  policy: PolicyMatrix | null
): { allowed: boolean; reason: string } {
  if (!policy) return { allowed: false, reason: 'Support policy not loaded yet.' };
  if (engine && ENGINE_BLOCKED_AUTOMATIC.has(engine)) {
    return { allowed: false, reason: `${engine} has no safe automatic cutover contract.` };
  }
  if (provider && !policy.supportedTrafficProviders.includes(provider)) {
    return {
      allowed: false,
      reason: `${provider} has no fenced switcher — automatic cutover is not available for this provider.`
    };
  }
  if (!provider) {
    return { allowed: false, reason: 'A fenced traffic provider (nginx/haproxy/caddy) is required for automatic cutover.' };
  }
  return { allowed: true, reason: 'Automatic cutover is permitted for this engine + provider.' };
}

// Tailwind class pairs (color + text) — never color alone; labels carry meaning.
export function supportClasses(level: SupportLevel): string {
  switch (level) {
    case 'automatic': return 'bg-success/10 text-success border-success/30';
    case 'manual': return 'bg-info/10 text-info border-info/30';
    case 'degraded': return 'bg-warning/10 text-warning border-warning/30';
    case 'blocked': return 'bg-error/10 text-error border-error/30';
    case 'deferred': return 'bg-surface-muted text-fg-muted border-border';
    case 'staging-only': return 'bg-accent/10 text-accent border-accent/30';
    default: return 'bg-surface-muted text-fg-muted border-border';
  }
}
