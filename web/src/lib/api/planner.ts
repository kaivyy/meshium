import { api } from '$lib/api/client';

// --- Plan Types (matching backend Phase 5) ---

export type RiskLevel = 'low' | 'medium' | 'high' | 'critical';
export type StepType = 'docker_volume' | 'docker_image' | 'database' | 'file' | 'config' | 'nginx' | 'service';

export interface ServerSummary {
  hostname: string;
  os: string;
  ramMB: number;
  diskGB: number;
}

export interface TransferEstimate {
  sizeBytes: number;
  durationMin: number;
  durationMax: number;
  confidence: number; // 0.0-1.0
}

export interface PlanWarning {
  code: string;
  message: string;
}

export interface PlanBlocker {
  code: string;
  message: string;
}

export interface PlannedStep {
  order: number;
  name: string;
  type: StepType;
  dependsOn: number[];
  estimate: TransferEstimate;
  riskLevel: RiskLevel;
  reversible: boolean;
  config: Record<string, unknown>;
}

export interface MigrationPlan {
  id: string;
  createdAt: string;
  source: ServerSummary;
  target: ServerSummary;
  steps: PlannedStep[];
  totalEstimate: TransferEstimate;
  riskLevel: RiskLevel;
  warnings: PlanWarning[];
  blockers: PlanBlocker[];
}

export interface MigrationPlanSummary {
  id: string;
  createdAt: string;
  sourceHost: string;
  targetHost: string;
  stepCount: number;
  riskLevel: RiskLevel;
  hasBlockers: boolean;
  totalSizeBytes: number;
}

// --- API ---

export const plannerApi = {
  list: () => api.get('/plans') as Promise<MigrationPlanSummary[]>,
  get: (id: string) => api.get(`/plans/${id}`) as Promise<MigrationPlan>,
  create: (sourceID: number, targetID: number) =>
    api.post('/plans', { sourceID, targetID }) as Promise<MigrationPlan>,
  delete: (id: string) => api.delete(`/plans/${id}`) as Promise<void>,
  execute: (id: string) =>
    api.post(`/plans/${id}/execute`, {}) as Promise<{ jobID: string }>,
};
