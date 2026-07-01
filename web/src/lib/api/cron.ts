import { api } from './client';

export interface CronJob {
  id: string;
  schedule: string;
  command: string;
  user: string;
  source: string;
  comment: string;
  enabled: boolean;
}

export interface CronJobRequest {
  schedule: string;
  command: string;
  comment?: string;
}

export async function listCronJobs(serverId: number): Promise<CronJob[]> {
  return api.get<CronJob[]>(`/servers/${serverId}/cron`);
}

export async function createCronJob(serverId: number, request: CronJobRequest): Promise<{ status: string }> {
  return api.post<{ status: string }>(`/servers/${serverId}/cron`, request);
}

export async function updateCronJob(
  serverId: number,
  jobId: string,
  request: CronJobRequest
): Promise<{ status: string }> {
  return api.put<{ status: string }>(`/servers/${serverId}/cron/${jobId}`, request);
}

export async function deleteCronJob(serverId: number, jobId: string): Promise<{ status: string }> {
  return api.delete<{ status: string }>(`/servers/${serverId}/cron/${jobId}`);
}
