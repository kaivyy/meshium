import { writable } from 'svelte/store';
import { migrationApi, type MigrationPlan, type MigrationStep } from '$lib/api/migrations';

export const migrations = writable<MigrationPlan[]>([]);
export const currentMigration = writable<MigrationPlan | null>(null);
export const migrationSteps = writable<MigrationStep[]>([]);
export const loading = writable(false);
export const error = writable<string | null>(null);

export async function loadMigrations() {
  loading.set(true);
  error.set(null);
  try {
    const data = await migrationApi.list();
    migrations.set(data);
  } catch (e: any) {
    error.set(e.message || 'Failed to load migrations');
  } finally {
    loading.set(false);
  }
}

export async function loadMigration(id: number) {
  loading.set(true);
  error.set(null);
  try {
    const [plan, steps] = await Promise.all([
      migrationApi.get(id),
      migrationApi.getSteps(id),
    ]);
    currentMigration.set(plan);
    migrationSteps.set(steps);
  } catch (e: any) {
    error.set(e.message || 'Failed to load migration');
  } finally {
    loading.set(false);
  }
}

export async function deleteMigration(id: number) {
  try {
    await migrationApi.delete(id);
    migrations.update(m => m.filter(mig => mig.id !== id));
  } catch (e: any) {
    error.set(e.message || 'Failed to delete migration');
  }
}

export function resetMigration() {
  currentMigration.set(null);
  migrationSteps.set([]);
  error.set(null);
}
