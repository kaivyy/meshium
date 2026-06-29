import { writable } from 'svelte/store';
import { jobsApi, wsJobProgress, type Job, type JobRequest, type JobFilter, type JobLog, type JobWSMessage } from '$lib/api/jobs';

export const jobs = writable<Job[]>([]);
export const currentJob = writable<Job | null>(null);
export const jobLogs = writable<JobLog[]>([]);
export const loading = writable(false);
export const error = writable<string | null>(null);

export async function loadJobs(filter?: JobFilter) {
  loading.set(true);
  error.set(null);
  try {
    const data = await jobsApi.list(filter);
    jobs.set(data);
  } catch (e: any) {
    error.set(e?.message || 'Failed to load jobs');
  } finally {
    loading.set(false);
  }
}

export async function loadJob(id: string) {
  loading.set(true);
  error.set(null);
  try {
    const job = await jobsApi.get(id);
    currentJob.set(job);
    const logs = await jobsApi.getLogs(id);
    jobLogs.set(logs);
  } catch (e: any) {
    error.set(e?.message || 'Failed to load job');
  } finally {
    loading.set(false);
  }
}

export async function submitJob(req: JobRequest): Promise<Job | null> {
  try {
    const job = await jobsApi.submit(req);
    jobs.update((list) => [...list, job]);
    return job;
  } catch (e: any) {
    error.set(e?.message || 'Failed to submit job');
    return null;
  }
}

export async function cancelJob(id: string) {
  try {
    await jobsApi.cancel(id);
    jobs.update((list) =>
      list.map((j) => (j.id === id ? { ...j, status: 'cancelled' as const } : j))
    );
  } catch (e: any) {
    error.set(e?.message || 'Failed to cancel job');
  }
}

export async function pauseJob(id: string) {
  try {
    await jobsApi.pause(id);
    jobs.update((list) =>
      list.map((j) => (j.id === id ? { ...j, status: 'paused' as const } : j))
    );
  } catch (e: any) {
    error.set(e?.message || 'Failed to pause job');
  }
}

export async function resumeJob(id: string) {
  try {
    await jobsApi.resume(id);
    jobs.update((list) =>
      list.map((j) => (j.id === id ? { ...j, status: 'running' as const } : j))
    );
  } catch (e: any) {
    error.set(e?.message || 'Failed to resume job');
  }
}

export function subscribeToJobProgress(
  jobID: string,
  onProgress: (job: Job) => void,
  onLog: (log: JobLog) => void
): WebSocket {
  return wsJobProgress(
    jobID,
    (msg: JobWSMessage) => {
      if (msg.type === 'progress' && msg.progress) {
        currentJob.update((j) => (j ? { ...j, progress: msg.progress! } : j));
        onProgress({ ...({} as Job), progress: msg.progress! });
      }
      // Note: logs are fetched via getLogs, not streamed in progress messages
    },
    undefined,
    undefined
  );
}

export function resetJob() {
  currentJob.set(null);
  jobLogs.set([]);
  error.set(null);
}

export function activeJobCount(jobsList: Job[]): number {
  return jobsList.filter((j) => j.status === 'running' || j.status === 'queued').length;
}
