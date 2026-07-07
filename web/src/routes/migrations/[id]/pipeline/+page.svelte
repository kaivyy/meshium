<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { page } from '$app/stores';
  import {
    pipelineApi, wsPipelineConnect, WIZARD_STEPS, defaultMigrationConfig,
    type MigrationSession, type PipelineStage, type WSMessageExtended,
    type CompatibilityCheckResult, type RiskReport, type HealthCheckResult,
    type SyncSession, type ReplicationStatus, type QueueState,
    type ProvisionState, type ContainerHealthInfo, type AuditEntry,
    type MigrationConfig, type WSConnectionState, type PlannerResult,
    type MigrationEvent, type PlannerWarning, type DependencyGraph,
    type StrategySelection
  } from '$lib/api/pipeline';
  import { APIError } from '$lib/api/client';
  import { migrationApi, type DryRunResult } from '$lib/api/migrations';
  import { toast } from '$lib/stores/toast';
  import PlannerView from '$lib/components/PlannerView.svelte';
  import LiveMonitor from '$lib/components/LiveMonitor.svelte';
  import MigrationHeader from '$lib/components/MigrationHeader.svelte';
  import PipelineStepper from '$lib/components/PipelineStepper.svelte';
  import CutoverChecklist from '$lib/components/CutoverChecklist.svelte';
  import ObservationPanel from '$lib/components/ObservationPanel.svelte';
  import BottomTabs from '$lib/components/BottomTabs.svelte';

  const migrationId = parseInt($page.params.id ?? '0', 10);

  // ── Wizard Step State ──
  let currentStep = 0;
  let stepStatuses: ('pending' | 'running' | 'completed' | 'failed')[] = Array(11).fill('pending');

  // ── Session & Data ──
  let session: MigrationSession | null = null;
  let loading = true;
  let config: MigrationConfig = defaultMigrationConfig();

  // ── Per-Step Data ──
  let compatibilityResults: CompatibilityCheckResult[] = [];
  let riskReport: RiskReport | null = null;
  let healthResults: HealthCheckResult[] = [];
  let syncSessions: SyncSession[] = [];
  let replicationStatus: ReplicationStatus[] = [];
  let queueStates: QueueState[] = [];
  let provisionStates: ProvisionState[] = [];
  let containerHealth: ContainerHealthInfo[] = [];
  let auditTrail: AuditEntry[] = [];
  let dryRunResult: DryRunResult | null = null;
  let plannerResult: PlannerResult | null = null;
  let plannerLoading = false;
  let migrationEvents: MigrationEvent[] = [];
  let dependencyGraph: DependencyGraph | null = null;
  let strategy: StrategySelection | null = null;
  let warnings: PlannerWarning[] = [];

  // ── Live Metrics ──
  let pipelineRunning = false;
  let pipelinePaused = false;
  let progress = 0;
  let bytesDone = 0;
  let bytesTotal = 0;
  let speedBytes = 0;
  let eta = '';
  let replicationLag = 0;
  let healthScore = 100;
  let currentState = '';
  let cpuUsagePercent = 0;
  let ramUsedBytes = 0;
  let ramTotalBytes = 0;
  let diskUsedPercent = 0;
  let networkRxBytesSec = 0;
  let networkTxBytesSec = 0;
  let wsMessages: WSMessageExtended[] = [];
  let wsConnectionState: WSConnectionState = 'disconnected';
  let wsControl: { close: () => void } | null = null;

  // ── Cutover State ──
  let cutoverConfirmed = false;

  // ── Observation State ──
  let observationStart: number | null = null;
  let observationDuration = 300;
  let observationElapsed = 0;
  let observationTimer: ReturnType<typeof setInterval> | null = null;

  // ── Step Action Loading ──
  let actionLoading = false;
  type ConfirmationAction = 'cutover' | 'rollback';
  let confirmationAction: ConfirmationAction | null = null;
  const trafficConfigPlaceholder = '{"configPath":"/etc/nginx/conf.d/app.conf","newServer":"10.0.0.2:80"}';

  onMount(async () => {
    await loadSession().catch(() => {});
  });

  onDestroy(() => {
    wsControl?.close();
    if (observationTimer) clearInterval(observationTimer);
  });

  // ══════════════════════════════════════════════════════
  //  DATA LOADING
  // ══════════════════════════════════════════════════════

  async function loadSession() {
    loading = true;
    try {
      session = await pipelineApi.getSession(migrationId);
      if (session?.config) {
        config = session.config;
        if (session.config.observationDuration) observationDuration = session.config.observationDuration;
      }
      // The create wizard (/ws/plan) never writes a MigrationConfig, so config
      // arrives empty and the user is forced to reselect categories at step 3.
      // Seed categories from the migration plan when the config has none.
      if (config.categories.length === 0 && session?.migration?.categories?.length) {
        config = { ...config, categories: [...session.migration.categories] };
      }
      if (session?.riskReport) riskReport = session.riskReport;
      if (session?.compatibilityResults) compatibilityResults = session.compatibilityResults;
      if (session?.healthHistory) healthResults = session.healthHistory;
      if (session?.syncSessions) syncSessions = session.syncSessions;
      if (session?.replicationStatus) replicationStatus = session.replicationStatus;
      if (session?.queueStates) queueStates = session.queueStates;
      if (session?.provisionStates) provisionStates = session.provisionStates;
      if (session?.auditTrail) auditTrail = session.auditTrail;
      if (session?.events) migrationEvents = session.events;
      currentState = session?.state || '';
      recoverStepFromState();
      recoverStepFromRecords();
      // Load planner data in background
      loadPlannerResult().catch(() => {});
    } catch {
      try {
        const plan = await migrationApi.get(migrationId);
        // MigrationPlan uses sourceServerId/targetServerId, but the page reads
        // session.migration.sourceId/targetId — map the field names so Discovery
        // shows servers and recoverStepFromRecords can markCompleted(0).
        session = {
          migration: {
            id: plan.id,
            sourceId: plan.sourceServerId,
            targetId: plan.targetServerId,
            categories: plan.categories ?? [],
            status: plan.status,
            state: plan.status,
            riskScore: 0,
            riskClass: '',
            error: plan.errorMessage,
            createdAt: plan.createdAt,
            completedAt: plan.completedAt,
          },
          state: plan.status,
        };
        currentState = plan.status;
        recoverStepFromRecords();
      } catch { /* ignore */ }
    } finally {
      loading = false;
    }
  }

  async function loadPlannerResult() {
    plannerLoading = true;
    try {
      plannerResult = await pipelineApi.getPlannerResult(migrationId);
      if (plannerResult) {
        dependencyGraph = plannerResult.dependencyGraph;
        warnings = plannerResult.warnings;
        if (plannerResult.strategy) strategy = plannerResult.strategy;
      }
    } catch { /* ignore */ } finally {
      plannerLoading = false;
    }
  }

  function stopObservationTimer() {
    if (observationTimer) clearInterval(observationTimer);
    observationTimer = null;
    observationStart = null;
    observationElapsed = 0;
  }

  function recoverStepFromState() {
    if (!session) return;
    const st = currentState.toLowerCase();
    if (['completed', 'committed', 'archived', 'rolled_back', 'cancelled'].includes(st)) {
      stopObservationTimer();
      setStep(10);
      stepStatuses[9] = 'completed';
      stepStatuses[10] = 'completed';
      pipelineRunning = false;
      return;
    }
    if (['observation', 'post_verification'].includes(st)) { setStep(9); startObservationTimer(); return; }
    if (['traffic_switch', 'pre_cutover'].includes(st)) { setStep(8); return; }
    if (['live_replication', 'verification'].includes(st)) { setStep(7); pipelineRunning = true; return; }
    if (['initial_sync', 'install_dependencies', 'provision_target'].includes(st)) { setStep(6); pipelineRunning = true; return; }
    if (st === 'backup') { setStep(5); return; }
    if (st === 'risk_assessment') { setStep(2); return; }
    if (st === 'compatibility_check') { setStep(1); return; }
    if (st === 'discovery' || st === 'planning') { setStep(0); return; }
  }

  function recoverStepFromRecords() {
    if (!session) return;

    const restoredStatuses: ('pending' | 'running' | 'completed' | 'failed')[] = Array(11).fill('pending');
    const markCompleted = (step: number) => {
      for (let i = 0; i <= step; i++) restoredStatuses[i] = 'completed';
    };
    const markFailed = (step: number) => {
      for (let i = 0; i < step; i++) restoredStatuses[i] = 'completed';
      restoredStatuses[step] = 'failed';
    };

    const sessionSourceId = session.migration?.sourceId ?? null;
    const sessionTargetId = session.migration?.targetId ?? null;
    if (sessionSourceId && sessionTargetId) markCompleted(0);

    for (const stage of session.stages ?? []) {
      if (stage.state === 'failed') {
        const failedStep = wizardStepForStage(stage.stageName);
        if (failedStep !== null) markFailed(failedStep);
        continue;
      }
      if (stage.state !== 'completed' && stage.state !== 'skipped') continue;
      const completedStep = wizardStepForStage(stage.stageName);
      if (completedStep !== null) markCompleted(completedStep);
    }

    if (compatibilityResults.length > 0) {
      const hasCritical = compatibilityResults.some(r => r.severity === 'critical' && !r.passed);
      if (hasCritical) markFailed(1);
      else markCompleted(1);
    }

    if (riskReport) markCompleted(2);
    if (dryRunResult) markCompleted(4);
    if (provisionStates.length > 0 && provisionStates.every(p => p.verified || p.installed)) markCompleted(5);
    if (syncSessions.length > 0 || replicationStatus.length > 0) markCompleted(6);
    if (replicationStatus.length > 0 && replicationStatus.every(r => r.replicationLag <= 5 && r.status !== 'failed')) markCompleted(7);
    if (cutoverConfirmed || ['traffic_switch', 'post_verification', 'observation', 'committed', 'completed'].includes(currentState.toLowerCase())) markCompleted(8);
    if (['committed', 'completed', 'archived'].includes(currentState.toLowerCase())) markCompleted(10);

    const stateStep = currentStep;
    stepStatuses = restoredStatuses.map((status, i) => {
      if (status !== 'pending') return status;
      if (i === stateStep && stepStatuses[i] === 'running') return 'running';
      return status;
    });

    const lastCompleted = stepStatuses.reduce((last, status, i) => status === 'completed' ? i : last, -1);
    const failedStep = stepStatuses.findIndex(status => status === 'failed');
    if (failedStep >= 0) {
      currentStep = failedStep;
      return;
    }
    if (lastCompleted >= currentStep && currentStep < 10) {
      currentStep = Math.min(lastCompleted + 1, 10);
      if (stepStatuses[currentStep] === 'pending') stepStatuses[currentStep] = 'running';
    }
  }

  function wizardStepForStage(stageName: string): number | null {
    switch (stageName) {
      case 'discovery': return 0;
      case 'analysis':
      case 'validation': return 1;
      case 'planning': return 3;
      case 'preparation': return 5;
      case 'initial_sync': return 6;
      case 'live_replication':
      case 'health_verification': return 7;
      case 'pre_cutover_validation':
      case 'traffic_switch': return 8;
      case 'post_cutover_observation': return 9;
      case 'finalization':
      case 'archive': return 10;
      default: return null;
    }
  }

  function setStep(step: number) {
    currentStep = step;
    for (let i = 0; i < step; i++) {
      if (stepStatuses[i] === 'pending') stepStatuses[i] = 'completed';
    }
    if (stepStatuses[step] === 'pending') stepStatuses[step] = 'running';
  }

  // ══════════════════════════════════════════════════════
  //  NAVIGATION & VALIDATION
  // ══════════════════════════════════════════════════════

  function canProceed(step: number): boolean {
    switch (step) {
      case 0: return stepStatuses[0] === 'completed';
      case 1: return stepStatuses[1] === 'completed' && !compatibilityResults.some(r => r.severity === 'critical' && !r.passed);
      case 2: return stepStatuses[2] === 'completed' && riskReport !== null;
      case 3: return config.categories.length > 0;
      case 4: return stepStatuses[4] === 'completed';
      case 5: return stepStatuses[5] === 'completed';
      case 6: return pipelineRunning;
      // Step 7 is only auto-marked 'completed' by the WS transition, which also
      // advances the step — so gating on that status alone leaves no manual path
      // if the WS never emits pre_cutover/traffic_switch. Allow proceeding when
      // replication has actually caught up (lag within threshold and no failed
      // replicas), which is the real precondition for cutover.
      case 7: {
        if (stepStatuses[7] === 'completed') return true;
        if (replicationLag > 5) return false;
        if (replicationStatus.length > 0) {
          return replicationStatus.every(r => r.replicationLag <= 5 && r.status !== 'failed');
        }
        return pipelineRunning;
      }
      case 8: return stepStatuses[8] === 'completed';
      case 9: return stepStatuses[9] === 'completed';
      default: return false;
    }
  }

  async function savePlanConfig(showToast = false): Promise<boolean> {
    try {
      config = await pipelineApi.configure(migrationId, config);
      if (showToast) toast.success('Migration plan saved');
      return true;
    } catch (err) {
      toast.error(actionErrorMessage(err, 'Failed to save migration plan'));
      return false;
    }
  }

  async function goNext() {
    if (currentStep < 10 && canProceed(currentStep)) {
      if (currentStep === 3) {
        actionLoading = true;
        const saved = await savePlanConfig(true);
        actionLoading = false;
        if (!saved) return;
      }
      currentStep++;
      if (stepStatuses[currentStep] === 'pending') stepStatuses[currentStep] = 'running';
    }
  }

  function goBack() {
    if (currentStep > 0 && !pipelineRunning) currentStep--;
  }

  function goToStep(step: number) {
    if (step < currentStep && !pipelineRunning) currentStep = step;
  }

  // ══════════════════════════════════════════════════════
  //  STEP ACTIONS
  // ══════════════════════════════════════════════════════

  async function runDiscovery() {
    // No dedicated discovery endpoint exists; discovery data is produced by the
    // planner and surfaced via the session. Re-load the session and reflect
    // whether source/target discovery data is actually present rather than
    // declaring success unconditionally.
    actionLoading = true;
    stepStatuses[0] = 'running';
    try {
      await loadSession().catch(() => {});
      const hasServers = !!session?.migration?.sourceId && !!session?.migration?.targetId;
      if (hasServers) {
        stepStatuses[0] = 'completed';
        toast.success('Discovery data loaded');
      } else {
        stepStatuses[0] = 'failed';
        toast.error('No discovery data found for source/target servers');
      }
    } catch {
      stepStatuses[0] = 'failed';
      toast.error('Discovery failed');
    } finally { actionLoading = false; }
  }

  async function runCompatibilityCheck() {
    actionLoading = true;
    stepStatuses[1] = 'running';
    try {
      compatibilityResults = await pipelineApi.checkCompatibility(migrationId);
      const hasCritical = compatibilityResults.some(r => r.severity === 'critical' && !r.passed);
      stepStatuses[1] = hasCritical ? 'failed' : 'completed';
      if (hasCritical) toast.error('Critical compatibility issues found');
      else toast.success('Compatibility check passed');
    } catch {
      stepStatuses[1] = 'failed';
      toast.error('Compatibility check failed');
    } finally { actionLoading = false; }
  }

  async function runRiskAssessment() {
    actionLoading = true;
    stepStatuses[2] = 'running';
    try {
      riskReport = await pipelineApi.assessRisk(migrationId);
      stepStatuses[2] = 'completed';
      toast.success('Risk assessment completed');
    } catch {
      stepStatuses[2] = 'failed';
      toast.error('Risk assessment failed');
    } finally { actionLoading = false; }
  }

  async function runDryRun() {
    actionLoading = true;
    stepStatuses[4] = 'running';
    try {
      dryRunResult = await migrationApi.dryRun(migrationId);
      stepStatuses[4] = 'completed';
      toast.success('Dry run completed');
    } catch {
      stepStatuses[4] = 'failed';
      toast.error('Dry run failed');
    } finally { actionLoading = false; }
  }

  async function runProvision() {
    actionLoading = true;
    stepStatuses[5] = 'running';
    try {
      // The provision endpoint returns the current provision states rather than a
      // success flag, so trust the states — not the mere fact the call succeeded.
      provisionStates = await pipelineApi.provision(migrationId);
      const anyFailed = provisionStates.some(p => p.error);
      const ready = provisionStates.length > 0 && provisionStates.every(p => p.verified || p.installed);
      if (anyFailed) {
        stepStatuses[5] = 'failed';
        toast.error('Provisioning reported errors on the target');
      } else if (ready) {
        stepStatuses[5] = 'completed';
        toast.success('Provisioning completed');
      } else {
        // Nothing was actually provisioned yet — don't claim success.
        stepStatuses[5] = 'pending';
        toast.info('No provisioning changes reported. Verify target dependencies before proceeding.');
      }
    } catch {
      stepStatuses[5] = 'failed';
      toast.error('Provisioning failed');
    } finally { actionLoading = false; }
  }

  async function startPipeline() {
    actionLoading = true;
    const saved = await savePlanConfig(false);
    actionLoading = false;
    if (!saved) return;

    pipelineRunning = true;
    stepStatuses[6] = 'running';
    wsMessages = [];
    toast.info('Pipeline started');
    if (wsControl) { wsControl.close(); wsControl = null; }
    wsControl = wsPipelineConnect(migrationId, (msg: WSMessageExtended) => {
      wsMessages = [...wsMessages, msg];
      updateLiveMetrics(msg);
      if (msg.currentState) {
        const st = msg.currentState.toLowerCase();
        if (st === 'live_replication' && currentStep === 6) {
          stepStatuses[6] = 'completed'; currentStep = 7; stepStatuses[7] = 'running';
        }
        if ((st === 'pre_cutover' || st === 'traffic_switch') && currentStep === 7) {
          stepStatuses[7] = 'completed'; currentStep = 8; stepStatuses[8] = 'running';
        }
        if ((st === 'post_verification' || st === 'observation') && currentStep === 8) {
          stepStatuses[8] = 'completed'; currentStep = 9; stepStatuses[9] = 'running';
          startObservationTimer();
        }
        if (st === 'committed' || st === 'completed') {
          stepStatuses[9] = 'completed'; currentStep = 10; stepStatuses[10] = 'completed';
          pipelineRunning = false;
        }
      }
      if (msg.status === 'error') {
        pipelineRunning = false;
        toast.error(`Pipeline error: ${msg.error || 'Unknown error'}`);
      }
    }, (status: WSConnectionState) => {
      wsConnectionState = status;
      if (status === 'failed') { pipelineRunning = false; toast.error('WebSocket connection lost.'); }
    });
  }

  function updateLiveMetrics(msg: WSMessageExtended) {
    if (msg.progress !== undefined) progress = msg.progress;
    if (msg.bytesDone !== undefined) bytesDone = msg.bytesDone;
    if (msg.bytesTotal) bytesTotal = msg.bytesTotal;
    if (msg.speedBytes) speedBytes = msg.speedBytes;
    if (msg.replicationLag !== undefined) replicationLag = msg.replicationLag;
    if (msg.healthScore !== undefined) healthScore = msg.healthScore;
    if (msg.eta) eta = msg.eta;
    if (msg.currentState) currentState = msg.currentState;
    if (msg.cpuUsagePercent !== undefined) cpuUsagePercent = msg.cpuUsagePercent;
    if (msg.ramUsedBytes !== undefined) ramUsedBytes = msg.ramUsedBytes;
    if (msg.ramTotalBytes) ramTotalBytes = msg.ramTotalBytes;
    if (msg.diskUsedPercent !== undefined) diskUsedPercent = msg.diskUsedPercent;
    if (msg.networkRxBytesSec !== undefined) networkRxBytesSec = msg.networkRxBytesSec;
    if (msg.networkTxBytesSec !== undefined) networkTxBytesSec = msg.networkTxBytesSec;
    if (msg.containerHealth) containerHealth = msg.containerHealth;
    if (msg.queueInfo) queueStates = msg.queueInfo;
  }

  function actionErrorMessage(err: unknown, fallback: string): string {
    if (err instanceof APIError) return err.message || fallback;
    if (err instanceof Error) return err.message || fallback;
    return fallback;
  }

  function isTerminalState(state: string): boolean {
    return ['completed', 'committed', 'archived', 'rolled_back', 'cancelled'].includes(state.toLowerCase());
  }

  function canRetryState(state: string): boolean {
    return ['failed', 'interrupted'].includes(state.toLowerCase());
  }

  function openConfirmation(action: ConfirmationAction) { confirmationAction = action; }
  function closeConfirmation() { if (!actionLoading) confirmationAction = null; }

  async function runConfirmedAction() {
    if (confirmationAction === 'rollback') { await rollbackPipeline(); return; }
    if (confirmationAction === 'cutover') { await confirmCutover(); }
  }

  async function confirmCutover() {
    cutoverConfirmed = true;
    actionLoading = true;
    try {
      await pipelineApi.cutover(migrationId);
      await loadSession().catch(() => {});
      toast.success('Cutover confirmed');
      confirmationAction = null;
    } catch (err) {
      cutoverConfirmed = false;
      toast.error(actionErrorMessage(err, 'Cutover failed'));
    } finally { actionLoading = false; }
  }

  // Resolve the real observation start time so the countdown reflects actual
  // elapsed time across reloads instead of restarting from Date.now() each time.
  // Prefer the observation stage's startedAt from the loaded session; fall back
  // to a localStorage value keyed by migration id (persisted on first start).
  function resolveObservationStart(): number {
    const storageKey = `meshium_obs_start_${migrationId}`;
    const stage = session?.stages?.find(
      s => s.stageName === 'post_cutover_observation' && s.startedAt
    );
    if (stage?.startedAt) {
      const parsed = Date.parse(stage.startedAt);
      if (!Number.isNaN(parsed)) {
        try { localStorage.setItem(storageKey, String(parsed)); } catch { /* ignore */ }
        return parsed;
      }
    }
    try {
      const saved = localStorage.getItem(storageKey);
      if (saved) {
        const parsed = parseInt(saved, 10);
        if (!Number.isNaN(parsed)) return parsed;
      }
    } catch { /* ignore */ }
    // No persisted start available (e.g. first entry via WS): use now and persist it.
    const now = Date.now();
    try { localStorage.setItem(storageKey, String(now)); } catch { /* ignore */ }
    return now;
  }

  function startObservationTimer() {
    observationStart = resolveObservationStart();
    observationElapsed = Math.floor((Date.now() - observationStart) / 1000);
    if (observationTimer) clearInterval(observationTimer);
    observationTimer = setInterval(() => {
      observationElapsed = Math.floor((Date.now() - (observationStart || Date.now())) / 1000);
      if (observationElapsed >= observationDuration && stepStatuses[9] !== 'completed') {
        stepStatuses[9] = 'completed';
        if (observationTimer) clearInterval(observationTimer);
      }
    }, 1000);
  }

  async function commitMigration() {
    actionLoading = true;
    try {
      await pipelineApi.commit(migrationId);
      await loadSession().catch(() => {});
      toast.success('Migration committed successfully!');
    } catch (err) { toast.error(actionErrorMessage(err, 'Commit failed')); }
    finally { actionLoading = false; }
  }

  async function pausePipeline() {
    actionLoading = true;
    try {
      await pipelineApi.pause(migrationId);
      pipelinePaused = true;
      await loadSession().catch(() => {});
      toast.success('Pipeline paused');
    } catch (err) { toast.error(actionErrorMessage(err, 'Pause failed')); }
    finally { actionLoading = false; }
  }

  async function resumePipeline() {
    actionLoading = true;
    try {
      await pipelineApi.resume(migrationId);
      pipelinePaused = false;
      await loadSession().catch(() => {});
      toast.success('Pipeline resumed');
    } catch (err) { toast.error(actionErrorMessage(err, 'Resume failed')); }
    finally { actionLoading = false; }
  }

  async function rollbackPipeline() {
    actionLoading = true;
    try {
      await pipelineApi.rollbackMigration(migrationId);
      pipelineRunning = false; pipelinePaused = false;
      await loadSession().catch(() => {});
      toast.success('Rollback initiated');
      confirmationAction = null;
    } catch (err) { toast.error(actionErrorMessage(err, 'Rollback failed')); }
    finally { actionLoading = false; }
  }

  async function exportReport() {
    try {
      const blob = await pipelineApi.exportReport(migrationId);
      const url = URL.createObjectURL(blob as any);
      const a = document.createElement('a');
      a.href = url; a.download = `migration-${migrationId}-report.json`; a.click();
      URL.revokeObjectURL(url);
      toast.success('Report exported');
    } catch { toast.error('Export failed'); }
  }

  async function refreshHealth() {
    try { healthResults = await pipelineApi.checkHealth(migrationId); } catch { /* ignore */ }
  }

  async function refreshQueue() {
    try { queueStates = await pipelineApi.getQueueStates(migrationId); } catch { /* ignore */ }
  }

  async function retryPipeline() {
    actionLoading = true;
    try {
      await pipelineApi.retry(migrationId);
      await loadSession().catch(() => {});
      toast.success('Pipeline retried');
    } catch (err) { toast.error(actionErrorMessage(err, 'Retry failed')); }
    finally { actionLoading = false; }
  }

  async function refreshAudit() {
    try { auditTrail = await pipelineApi.getAuditTrail(migrationId); } catch { /* ignore */ }
  }

  // ══════════════════════════════════════════════════════
  //  HELPERS
  // ══════════════════════════════════════════════════════

  function formatBytes(bytes: number): string {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
  }

  function riskColor(cls: string): string {
    switch (cls) {
      case 'low': return 'text-success';
      case 'medium': return 'text-warning';
      case 'high': return 'text-warning';
      case 'critical': return 'text-error';
      default: return 'text-fg-subtle';
    }
  }

  function riskBg(cls: string): string {
    switch (cls) {
      case 'low': return 'bg-success/10 border-success/30';
      case 'medium': return 'bg-warning/10 border-warning/30';
      case 'high': return 'bg-warning/10 border-warning/30';
      case 'critical': return 'bg-error/10 border-error/30';
      default: return 'bg-surface-muted border-border';
    }
  }

  function severityColor(sev: string): string {
    switch (sev) {
      case 'critical': return 'bg-error/15 text-error border-error/30';
      case 'high': return 'bg-warning/15 text-warning border-warning/30';
      case 'warning': return 'bg-warning/15 text-warning border-warning/30';
      default: return 'bg-info/15 text-info border-info/30';
    }
  }

  const availableCategories = ['packages', 'configs', 'services', 'users', 'docker'];

  function toggleCategory(cat: string) {
    if (config.categories.includes(cat)) {
      config.categories = config.categories.filter(c => c !== cat);
    } else {
      config.categories = [...config.categories, cat];
    }
  }

  $: ramPercent = ramTotalBytes > 0 ? ((ramUsedBytes / ramTotalBytes) * 100) : 0;
  $: totalBandwidth = networkRxBytesSec + networkTxBytesSec;
  $: observationRemaining = Math.max(0, observationDuration - observationElapsed);
  $: observationProgress = observationDuration > 0 ? (observationElapsed / observationDuration) * 100 : 0;
  $: sourceId = session?.migration?.sourceId ?? null;
  $: targetId = session?.migration?.targetId ?? null;
  $: rollbackAvailable = !isTerminalState(currentState) && currentStep >= 6;
</script>

<div class="h-full flex flex-col bg-bg text-fg">
  <!-- ═══ HEADER ═══ -->
  <MigrationHeader
    {migrationId}
    {currentState}
    {sourceId}
    {targetId}
    {healthScore}
    {riskReport}
    {strategy}
    {rollbackAvailable}
    {pipelineRunning}
    {pipelinePaused}
    {wsConnectionState}
  />

  <!-- ═══ PIPELINE STEPPER ═══ -->
  <PipelineStepper {currentStep} {stepStatuses} {pipelineRunning} />

  <!-- ═══ ACTION BAR ═══ -->
  <div class="border-b border-border px-3 sm:px-4 py-2 flex items-center justify-between shrink-0 gap-2 overflow-x-auto">
    <div class="flex items-center gap-2">
      {#if currentStep > 0 && !pipelineRunning}
        <button on:click={goBack} class="flex items-center gap-1.5 px-3 py-1.5 bg-surface-muted hover:bg-surface rounded-lg text-xs font-medium transition-colors">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m12 19-7-7 7-7"/><path d="M19 12H5"/></svg>
          Back
        </button>
      {/if}
      {#if currentStep < 10}
        <button on:click={goNext} disabled={!canProceed(currentStep) || actionLoading} class="flex items-center gap-1.5 px-3 py-1.5 bg-accent hover:bg-accent-hover text-accent-fg disabled:opacity-40 disabled:cursor-not-allowed rounded-lg text-xs font-medium transition-colors">
          {currentStep === 7 && replicationLag <= 5 ? 'Ready for Cutover' : 'Next'}
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m5 12h14"/><path d="m12 5 7 7-7 7"/></svg>
        </button>
      {/if}
    </div>
    <div class="flex items-center gap-2">
      {#if pipelineRunning && !pipelinePaused}
        <button on:click={pausePipeline} class="flex items-center gap-1.5 px-3 py-1.5 bg-warning hover:bg-warning/90 text-accent-fg rounded-lg text-xs font-medium transition-colors">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="6" y="4" width="4" height="16"/><rect x="14" y="4" width="4" height="16"/></svg>
          Pause
        </button>
      {/if}
      {#if pipelinePaused}
        <button on:click={resumePipeline} class="flex items-center gap-1.5 px-3 py-1.5 bg-accent hover:bg-accent-hover text-accent-fg rounded-lg text-xs font-medium transition-colors">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polygon points="6 3 20 12 6 21 6 3"/></svg>
          Resume
        </button>
      {/if}
      {#if canRetryState(currentState)}
        <button on:click={retryPipeline} class="flex items-center gap-1.5 px-3 py-1.5 bg-info hover:bg-info/90 text-accent-fg rounded-lg text-xs font-medium transition-colors">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 2v6h6"/><path d="M3 8a9 9 0 1 1 3.5 7"/></svg>
          Retry
        </button>
      {/if}
      {#if !isTerminalState(currentState)}
        <button on:click={() => openConfirmation('rollback')} class="flex items-center gap-1.5 px-3 py-1.5 bg-error hover:bg-error/90 text-accent-fg rounded-lg text-xs font-medium transition-colors">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/></svg>
          Rollback
        </button>
      {/if}
      <button on:click={exportReport} class="flex items-center gap-1.5 px-3 py-1.5 bg-surface-muted hover:bg-surface rounded-lg text-xs font-medium transition-colors">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>
        Export
      </button>
      <a href="/migrations/{migrationId}/diff" class="flex items-center gap-1.5 px-3 py-1.5 bg-surface-muted hover:bg-surface rounded-lg text-xs font-medium transition-colors">
        Diff
      </a>
    </div>
  </div>

  <!-- ═══ MAIN CONTENT AREA (responsive) ═══ -->
  <div class="flex-1 flex flex-col lg:flex-row overflow-hidden">
    <!-- Center Workspace -->
    <div class="flex-1 overflow-y-auto p-3 sm:p-4">
      {#if loading}
        <div class="flex items-center justify-center h-64 text-fg-subtle">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="animate-spin mr-3"><path d="M21 12a9 9 0 1 1-6.219-8.56"/></svg>
          Loading migration data...
        </div>

      {:else if currentStep === 0}
        <!-- DISCOVERY -->
        <div class="bg-surface rounded-xl p-4 sm:p-6 border border-border mb-4">
          <div class="flex items-center gap-3 mb-5">
            <div class="w-10 h-10 rounded-lg bg-info/10 flex items-center justify-center">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-info"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/></svg>
            </div>
            <div>
              <h2 class="text-lg font-semibold">Discovery</h2>
              <p class="text-sm text-fg-subtle">Discover services, containers, and databases on source &amp; target servers</p>
            </div>
          </div>
          {#if stepStatuses[0] === 'pending' || stepStatuses[0] === 'failed'}
            <div class="text-center py-8">
              <p class="text-fg-subtle mb-4">Run discovery to collect information about your source and target servers.</p>
              <button on:click={runDiscovery} disabled={actionLoading} class="px-6 py-2.5 bg-accent hover:bg-accent-hover text-accent-fg disabled:opacity-50 rounded-lg font-medium transition-colors">
                {actionLoading ? 'Running Discovery...' : 'Run Discovery'}
              </button>
            </div>
          {:else if stepStatuses[0] === 'running'}
            <div class="text-center py-8 text-fg-subtle">
              <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="animate-spin mx-auto mb-3"><path d="M21 12a9 9 0 1 1-6.219-8.56"/></svg>
              Discovering servers...
            </div>
          {:else}
            <div class="grid grid-cols-2 gap-4">
              <div class="bg-surface-muted rounded-lg p-4">
                <h3 class="text-sm font-medium text-fg-muted mb-3">Source Server</h3>
                <div class="space-y-2 text-sm">
                  <div class="flex justify-between"><span class="text-fg-subtle">ID</span><span>{sourceId ?? '—'}</span></div>
                  <div class="flex justify-between"><span class="text-fg-subtle">Status</span><span class="text-success">Connected</span></div>
                </div>
              </div>
              <div class="bg-surface-muted rounded-lg p-4">
                <h3 class="text-sm font-medium text-fg-muted mb-3">Target Server</h3>
                <div class="space-y-2 text-sm">
                  <div class="flex justify-between"><span class="text-fg-subtle">ID</span><span>{targetId ?? '—'}</span></div>
                  <div class="flex justify-between"><span class="text-fg-subtle">Status</span><span class="text-success">Connected</span></div>
                </div>
              </div>
            </div>
            {#if riskReport}
              <div class="mt-4 grid grid-cols-2 sm:grid-cols-4 gap-3">
                <div class="bg-surface-muted rounded-lg p-3 text-center"><div class="text-xs text-fg-subtle">Data Size</div><div class="font-mono text-sm">{formatBytes(riskReport.dataSizeBytes)}</div></div>
                <div class="bg-surface-muted rounded-lg p-3 text-center"><div class="text-xs text-fg-subtle">DB Size</div><div class="font-mono text-sm">{formatBytes(riskReport.databaseSizeBytes)}</div></div>
                <div class="bg-surface-muted rounded-lg p-3 text-center"><div class="text-xs text-fg-subtle">Containers</div><div class="font-mono text-sm">{riskReport.containerCount}</div></div>
                <div class="bg-surface-muted rounded-lg p-3 text-center"><div class="text-xs text-fg-subtle">Volumes</div><div class="font-mono text-sm">{riskReport.volumeCount}</div></div>
              </div>
            {/if}
          {/if}
        </div>

      {:else if currentStep === 1}
        <!-- COMPATIBILITY -->
        <div class="bg-surface rounded-xl p-4 sm:p-6 border border-border mb-4">
          <div class="flex items-center justify-between mb-5">
            <div class="flex items-center gap-3">
              <div class="w-10 h-10 rounded-lg bg-accent/10 flex items-center justify-center">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-accent"><path d="M12 3v18"/><rect width="18" height="18" x="3" y="3" rx="2"/></svg>
              </div>
              <div><h2 class="text-lg font-semibold">Compatibility Check</h2><p class="text-sm text-fg-subtle">Verify source and target are compatible for migration</p></div>
            </div>
            <button on:click={runCompatibilityCheck} disabled={actionLoading} class="px-4 py-2 bg-accent hover:bg-accent-hover text-accent-fg disabled:opacity-50 rounded-lg text-sm font-medium transition-colors">
              {actionLoading ? 'Checking...' : 'Run Check'}
            </button>
          </div>
          {#if compatibilityResults.length > 0}
            <div class="space-y-2">
              {#each compatibilityResults as result}
                <div class="bg-surface-muted rounded-lg p-3 flex items-center gap-4">
                  <div class="px-2 py-1 rounded text-xs font-medium border {severityColor(result.severity)}">{result.severity.toUpperCase()}</div>
                  <div class="flex-1"><div class="font-medium text-sm">{result.checkName}</div><div class="text-xs text-fg-subtle">{result.message}</div></div>
                  {#if result.passed}
                    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-success"><path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/><polyline points="22 4 12 14.01 9 11.01"/></svg>
                  {:else}
                    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-error"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>
                  {/if}
                </div>
              {/each}
            </div>
          {:else}
            <div class="text-center py-8 text-fg-subtle"><p>No compatibility check results yet.</p></div>
          {/if}
        </div>

      {:else if currentStep === 2}
        <!-- RISK ASSESSMENT -->
        <div class="bg-surface rounded-xl p-4 sm:p-6 border border-border mb-4">
          <div class="flex items-center justify-between mb-5">
            <div class="flex items-center gap-3">
              <div class="w-10 h-10 rounded-lg bg-warning/10 flex items-center justify-center">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-warning"><path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3"/><path d="M12 9v4"/><path d="M12 17h.01"/></svg>
              </div>
              <div><h2 class="text-lg font-semibold">Risk Assessment</h2><p class="text-sm text-fg-subtle">Evaluate migration risk score and estimated downtime</p></div>
            </div>
            <button on:click={runRiskAssessment} disabled={actionLoading} class="px-4 py-2 bg-accent hover:bg-accent-hover text-accent-fg disabled:opacity-50 rounded-lg text-sm font-medium transition-colors">
              {actionLoading ? 'Assessing...' : 'Assess Risk'}
            </button>
          </div>
          {#if riskReport}
            <div class="grid grid-cols-2 sm:grid-cols-4 gap-3 mb-5">
              <div class="bg-surface-muted rounded-xl p-4 text-center border {riskBg(riskReport.riskClass)}">
                <div class="text-3xl font-bold {riskColor(riskReport.riskClass)}">{riskReport.riskScore.toFixed(0)}</div>
                <div class="text-xs text-fg-subtle mt-1">Risk Score</div>
              </div>
              <div class="bg-surface-muted rounded-xl p-4 text-center">
                <div class="text-xl font-bold {riskColor(riskReport.riskClass)} uppercase">{riskReport.riskClass}</div>
                <div class="text-xs text-fg-subtle mt-1">Classification</div>
              </div>
              <div class="bg-surface-muted rounded-xl p-4 text-center">
                <div class="text-xl font-bold">{riskReport.downtimeEstimate || 'N/A'}</div>
                <div class="text-xs text-fg-subtle mt-1">Est. Downtime</div>
              </div>
              <div class="bg-surface-muted rounded-xl p-4 text-center">
                <div class="text-xl font-bold">{riskReport.rollbackComplexity || 'N/A'}</div>
                <div class="text-xs text-fg-subtle mt-1">Rollback Complexity</div>
              </div>
            </div>
          {:else}
            <div class="text-center py-8 text-fg-subtle"><p>No risk assessment yet.</p></div>
          {/if}
          <div class="mt-4">
            <div class="flex items-center justify-between mb-3">
              <h3 class="text-sm font-semibold text-fg-subtle uppercase">Workload Analysis & Migration Strategy</h3>
              <button on:click={loadPlannerResult} class="px-3 py-1 text-xs bg-surface-muted hover:bg-surface rounded-lg">Refresh Analysis</button>
            </div>
            <PlannerView {plannerResult} loading={plannerLoading} />
          </div>
        </div>

      {:else if currentStep === 3}
        <!-- PLAN -->
        <div class="bg-surface rounded-xl p-4 sm:p-6 border border-border mb-4">
          <div class="flex items-center gap-3 mb-5">
            <div class="w-10 h-10 rounded-lg bg-info/10 flex items-center justify-center">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-info"><path d="M12 20h9"/><path d="M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4L16.5 3.5z"/></svg>
            </div>
            <div><h2 class="text-lg font-semibold">Migration Plan</h2><p class="text-sm text-fg-subtle">Select categories and configure migration options</p></div>
          </div>
          <div class="mb-5">
            <h3 class="text-sm font-medium text-fg-muted mb-3">Categories</h3>
            <div class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 gap-2">
              {#each availableCategories as cat}
                <button on:click={() => toggleCategory(cat)} class="px-3 py-2 rounded-lg text-sm font-medium border transition-colors {config.categories.includes(cat) ? 'bg-accent/15 border-accent text-accent' : 'bg-surface-muted border-border-strong text-fg-subtle hover:border-border-strong'}">{cat}</button>
              {/each}
            </div>
            {#if config.categories.length === 0}<p class="text-xs text-error mt-2">Select at least one category to proceed.</p>{/if}
          </div>
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-5">
            <div>
              <h3 class="text-sm font-medium text-fg-muted mb-3">Replication</h3>
              <div class="space-y-3">
                <label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={config.replicationEnabled} class="rounded bg-surface-muted border-border-strong" />Enable Database Replication</label>
                {#if config.replicationEnabled}
                  <div><label for="replication-mode" class="text-xs text-fg-subtle">Mode</label>
                    <select id="replication-mode" bind:value={config.replicationMode} class="w-full mt-1 bg-surface-muted border border-border-strong rounded-lg px-3 py-2 text-sm">
                      <option value="streaming">Streaming</option><option value="logical">Logical</option><option value="snapshot">Snapshot</option>
                    </select>
                  </div>
                {/if}
              </div>
            </div>
            <div>
              <h3 class="text-sm font-medium text-fg-muted mb-3">Cutover</h3>
              <div class="space-y-3">
                <label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={config.freezeWriteOnCutover} class="rounded bg-surface-muted border-border-strong" />Freeze Writes on Cutover</label>
                <label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={config.drainQueuesOnCutover} class="rounded bg-surface-muted border-border-strong" />Drain Queues on Cutover</label>
              </div>
            </div>
            <div>
              <h3 class="text-sm font-medium text-fg-muted mb-3">Traffic</h3>
              <div class="space-y-3">
                <div><label for="traffic-provider" class="text-xs text-fg-subtle">Provider</label>
                  <select id="traffic-provider" bind:value={config.trafficProvider} class="w-full mt-1 bg-surface-muted border border-border-strong rounded-lg px-3 py-2 text-sm">
                    <option value="">Manual / none</option><option value="cloudflare">Cloudflare</option><option value="nginx">Nginx</option><option value="traefik">Traefik</option><option value="haproxy">HAProxy</option><option value="caddy">Caddy</option><option value="docker">Docker</option><option value="dns">DNS</option>
                  </select>
                </div>
                <div><label for="health-check-url" class="text-xs text-fg-subtle">Health Check URL</label>
                  <input id="health-check-url" bind:value={config.healthCheckUrl} placeholder="https://app.example.com/health" class="w-full mt-1 bg-surface-muted border border-border-strong rounded-lg px-3 py-2 text-sm" />
                </div>
                <div><label for="traffic-config" class="text-xs text-fg-subtle">Provider Config JSON</label>
                  <textarea id="traffic-config" bind:value={config.trafficConfig} rows="4" placeholder={trafficConfigPlaceholder} class="w-full mt-1 bg-surface-muted border border-border-strong rounded-lg px-3 py-2 text-xs font-mono"></textarea>
                </div>
              </div>
            </div>
            <div>
              <h3 class="text-sm font-medium text-fg-muted mb-3">Safety</h3>
              <div class="space-y-3">
                <label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={config.autoRollbackOnError} class="rounded bg-surface-muted border-border-strong" />Auto-Rollback on Error</label>
                <div><label for="max-error-rate" class="text-xs text-fg-subtle">Max Error Rate (%)</label>
                  <input id="max-error-rate" type="number" bind:value={config.maxErrorRate} min="0" max="100" class="w-full mt-1 bg-surface-muted border border-border-strong rounded-lg px-3 py-2 text-sm" />
                </div>
                <div><label for="observation-duration" class="text-xs text-fg-subtle">Observation Duration (seconds)</label>
                  <input id="observation-duration" type="number" bind:value={config.observationDuration} min="60" max="3600" class="w-full mt-1 bg-surface-muted border border-border-strong rounded-lg px-3 py-2 text-sm" />
                </div>
              </div>
            </div>
          </div>
        </div>

      {:else if currentStep === 4}
        <!-- DRY RUN -->
        <div class="bg-surface rounded-xl p-4 sm:p-6 border border-border mb-4">
          <div class="flex items-center justify-between mb-5">
            <div class="flex items-center gap-3">
              <div class="w-10 h-10 rounded-lg bg-info/10 flex items-center justify-center">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-info"><path d="M2 12s3-7 10-7 10 7 10 7-3 7-10 7-10-7-10-7Z"/><circle cx="12" cy="12" r="3"/></svg>
              </div>
              <div><h2 class="text-lg font-semibold">Dry Run</h2><p class="text-sm text-fg-subtle">Preview changes without applying them</p></div>
            </div>
            <button on:click={runDryRun} disabled={actionLoading} class="px-4 py-2 bg-accent hover:bg-accent-hover text-accent-fg disabled:opacity-50 rounded-lg text-sm font-medium transition-colors">
              {actionLoading ? 'Running...' : 'Run Dry Run'}
            </button>
          </div>
          {#if dryRunResult}
            <div class="grid grid-cols-2 sm:grid-cols-4 gap-3 mb-5">
              <div class="bg-surface-muted rounded-lg p-3 text-center"><div class="text-2xl font-bold">{dryRunResult.summary.totalChanges}</div><div class="text-xs text-fg-subtle">Total Changes</div></div>
              <div class="bg-surface-muted rounded-lg p-3 text-center"><div class="text-2xl font-bold text-success">{dryRunResult.summary.addCount}</div><div class="text-xs text-fg-subtle">Add</div></div>
              <div class="bg-surface-muted rounded-lg p-3 text-center"><div class="text-2xl font-bold text-warning">{dryRunResult.summary.modifyCount}</div><div class="text-xs text-fg-subtle">Modify</div></div>
              <div class="bg-surface-muted rounded-lg p-3 text-center"><div class="text-2xl font-bold text-error">{dryRunResult.summary.removeCount}</div><div class="text-xs text-fg-subtle">Remove</div></div>
            </div>
            <div class="space-y-2">
              {#each dryRunResult.categories as cat}
                <details class="bg-surface-muted rounded-lg">
                  <summary class="px-4 py-3 cursor-pointer text-sm font-medium flex items-center justify-between">
                    <span>{cat.category}</span><span class="text-xs text-fg-subtle">{cat.changes.length} changes &middot; {cat.summary}</span>
                  </summary>
                  <div class="px-4 pb-3 space-y-1">
                    {#each cat.changes as change}
                      <div class="text-xs flex items-center gap-2 py-1">
                        <span class="px-1.5 py-0.5 rounded font-mono {change.type === 'add' ? 'bg-success/15 text-success' : change.type === 'modify' ? 'bg-warning/15 text-warning' : 'bg-error/15 text-error'}">{change.type}</span>
                        <span class="text-fg-muted">{change.resource}</span><span class="text-fg-subtle">&mdash; {change.detail}</span>
                      </div>
                    {/each}
                  </div>
                </details>
              {/each}
            </div>
          {:else}
            <div class="text-center py-8 text-fg-subtle"><p>Run a dry run to preview what will change during migration.</p></div>
          {/if}
        </div>

      {:else if currentStep === 5}
        <!-- PROVISION -->
        <div class="bg-surface rounded-xl p-4 sm:p-6 border border-border mb-4">
          <div class="flex items-center justify-between mb-5">
            <div class="flex items-center gap-3">
              <div class="w-10 h-10 rounded-lg bg-accent/10 flex items-center justify-center">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-accent"><path d="m7.5 4.27 9 5.15"/><path d="M21 8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z"/><path d="m3.3 7 8.7 5 8.7-5"/><path d="M12 22V12"/></svg>
              </div>
              <div><h2 class="text-lg font-semibold">Provision Target</h2><p class="text-sm text-fg-subtle">Install dependencies on the target server</p></div>
            </div>
            <button on:click={runProvision} disabled={actionLoading} class="px-4 py-2 bg-accent hover:bg-accent-hover text-accent-fg disabled:opacity-50 rounded-lg text-sm font-medium transition-colors">
              {actionLoading ? 'Provisioning...' : 'Provision Target'}
            </button>
          </div>
          {#if provisionStates.length > 0}
            <div class="space-y-2">
              {#each provisionStates as ps}
                <div class="bg-surface-muted rounded-lg p-3 flex items-center gap-4">
                  <div class="w-8 h-8 rounded-full flex items-center justify-center {ps.verified ? 'bg-success/15' : ps.installed ? 'bg-warning/15' : 'bg-surface-muted'}">
                    {#if ps.verified}<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-success"><polyline points="20 6 9 17 4 12"/></svg>
                    {:else if ps.error}<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-error"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>
                    {:else}<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-fg-subtle animate-spin"><path d="M21 12a9 9 0 1 1-6.219-8.56"/></svg>{/if}
                  </div>
                  <div class="flex-1"><div class="font-medium text-sm">{ps.component}</div>{#if ps.error}<div class="text-xs text-error">{ps.error}</div>{/if}</div>
                  <div class="text-xs text-fg-subtle">{#if ps.verified}Verified{:else if ps.installed}Installed{:else}Pending{/if}{#if ps.version}&middot; {ps.version}{/if}</div>
                </div>
              {/each}
            </div>
          {:else}
            <div class="text-center py-8 text-fg-subtle"><p>Provision the target server with required dependencies.</p></div>
          {/if}
        </div>

      {:else if currentStep >= 6 && currentStep <= 9}
        <!-- EXECUTION PHASE -->
        <!-- Live Metrics Dashboard -->
        <div class="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-2 mb-4">
          <div class="bg-surface rounded-lg p-2.5 border border-border">
            <div class="text-xs text-fg-subtle mb-1">Progress</div>
            <div class="text-base font-mono">{progress.toFixed(1)}%</div>
            <div class="mt-1 h-1 bg-surface-muted rounded-full overflow-hidden"><div class="h-full bg-accent rounded-full" style="width: {progress}%"></div></div>
          </div>
          <div class="bg-surface rounded-lg p-2.5 border border-border">
            <div class="text-xs text-fg-subtle mb-1">Transfer</div>
            <div class="text-xs font-mono">{formatBytes(bytesDone)} / {formatBytes(bytesTotal)}</div>
          </div>
          <div class="bg-surface rounded-lg p-2.5 border border-border">
            <div class="text-xs text-fg-subtle mb-1">Speed</div>
            <div class="text-xs font-mono">{formatBytes(speedBytes)}/s</div>
            <div class="text-xs text-fg-subtle">&uarr;{formatBytes(networkTxBytesSec)}/s &darr;{formatBytes(networkRxBytesSec)}/s</div>
          </div>
          <div class="bg-surface rounded-lg p-2.5 border border-border">
            <div class="text-xs text-fg-subtle mb-1">Repl. Lag</div>
            <div class="text-base font-mono {replicationLag > 5 ? 'text-warning' : 'text-success'}">{replicationLag}s</div>
          </div>
          <div class="bg-surface rounded-lg p-2.5 border border-border">
            <div class="text-xs text-fg-subtle mb-1">Health</div>
            <div class="text-base font-mono {healthScore < 80 ? 'text-warning' : 'text-success'}">{healthScore.toFixed(0)}</div>
          </div>
          <div class="bg-surface rounded-lg p-2.5 border border-border">
            <div class="text-xs text-fg-subtle mb-1">ETA</div>
            <div class="text-base font-mono">{eta || '—'}</div>
          </div>
        </div>

        <!-- Resource Metrics -->
        <div class="grid grid-cols-1 sm:grid-cols-3 gap-2 mb-4">
          <div class="bg-surface rounded-lg p-2.5 border border-border">
            <div class="flex items-center justify-between mb-1"><span class="text-xs text-fg-subtle">CPU</span><span class="text-xs font-mono {cpuUsagePercent > 80 ? 'text-error' : cpuUsagePercent > 60 ? 'text-warning' : 'text-success'}">{cpuUsagePercent.toFixed(1)}%</span></div>
            <div class="h-1.5 bg-surface-muted rounded-full overflow-hidden"><div class="h-full rounded-full {cpuUsagePercent > 80 ? 'bg-error' : cpuUsagePercent > 60 ? 'bg-warning' : 'bg-success'}" style="width: {cpuUsagePercent}%"></div></div>
          </div>
          <div class="bg-surface rounded-lg p-2.5 border border-border">
            <div class="flex items-center justify-between mb-1"><span class="text-xs text-fg-subtle">RAM</span><span class="text-xs font-mono {ramPercent > 90 ? 'text-error' : ramPercent > 75 ? 'text-warning' : 'text-success'}">{formatBytes(ramUsedBytes)} / {formatBytes(ramTotalBytes)}</span></div>
            <div class="h-1.5 bg-surface-muted rounded-full overflow-hidden"><div class="h-full rounded-full {ramPercent > 90 ? 'bg-error' : ramPercent > 75 ? 'bg-warning' : 'bg-success'}" style="width: {ramPercent}%"></div></div>
          </div>
          <div class="bg-surface rounded-lg p-2.5 border border-border">
            <div class="flex items-center justify-between mb-1"><span class="text-xs text-fg-subtle">Disk</span><span class="text-xs font-mono {diskUsedPercent > 90 ? 'text-error' : 'text-warning'}">{diskUsedPercent.toFixed(1)}%</span></div>
            <div class="h-1.5 bg-surface-muted rounded-full overflow-hidden"><div class="h-full rounded-full {diskUsedPercent > 90 ? 'bg-error' : 'bg-warning'}" style="width: {diskUsedPercent}%"></div></div>
          </div>
        </div>

        <!-- Step-Specific Content -->
        {#if currentStep === 6}
          <div class="bg-surface rounded-xl p-4 sm:p-6 border border-border mb-4">
            <div class="flex items-center gap-3 mb-5">
              <div class="w-10 h-10 rounded-lg bg-success/10 flex items-center justify-center">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-success"><polygon points="6 3 20 12 6 21 6 3"/></svg>
              </div>
              <div><h2 class="text-lg font-semibold">Execute Migration</h2><p class="text-sm text-fg-subtle">Start initial sync and set up replication</p></div>
            </div>
            {#if !pipelineRunning}
              <div class="text-center py-6">
                <p class="text-fg-subtle mb-2">All pre-flight checks passed. Ready to start the migration pipeline.</p>
                <p class="text-xs text-fg-subtle mb-5">This will begin the initial data sync and set up live replication. You can pause or rollback at any time.</p>
                <button on:click={startPipeline} class="px-8 py-3 bg-success hover:bg-success/90 text-accent-fg rounded-lg font-medium text-lg transition-colors">Start Migration</button>
              </div>
            {:else}
              <div class="text-center py-4">
                <p class="text-success font-medium">Pipeline is running...</p>
                <p class="text-sm text-fg-subtle mt-1">Initial sync and replication setup in progress. Will auto-advance to Live Monitoring when replication is established.</p>
              </div>
            {/if}
          </div>

        {:else if currentStep === 7}
          <div class="bg-surface rounded-xl p-4 sm:p-6 border border-border mb-4">
            <div class="flex items-center justify-between mb-5">
              <div class="flex items-center gap-3">
                <div class="w-10 h-10 rounded-lg bg-info/10 flex items-center justify-center">
                  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-info"><path d="M22 12h-4l-3 9L9 3l-3 9H2"/></svg>
                </div>
                <div><h2 class="text-lg font-semibold">Live Monitoring</h2><p class="text-sm text-fg-subtle">Monitor sync progress and replication lag</p></div>
              </div>
              <div>
                {#if replicationLag <= 5}
                  <div class="px-4 py-2 bg-success/10 border border-success/30 rounded-lg text-success text-sm font-medium">Replication caught up ({replicationLag}s lag) &mdash; Ready for cutover</div>
                {:else}
                  <div class="px-4 py-2 bg-warning/10 border border-warning/30 rounded-lg text-warning text-sm font-medium">Waiting for replication... ({replicationLag}s lag, need &le;5s)</div>
                {/if}
              </div>
            </div>
            {#if replicationStatus.length > 0}
              <div class="space-y-2">
                {#each replicationStatus as rs}
                  <div class="bg-surface-muted rounded-lg p-3 flex items-center justify-between">
                    <div><div class="font-medium text-sm">{rs.databaseType}{rs.databaseName ? ` / ${rs.databaseName}` : ''}</div><div class="text-xs text-fg-subtle">{rs.sourceHost} &rarr; {rs.targetHost} &middot; {rs.replicationMode}</div></div>
                    <div class="text-right"><div class="text-lg font-bold {rs.replicationLag > 5 ? 'text-warning' : 'text-success'}">{rs.replicationLag}s</div><div class="text-xs text-fg-subtle">{rs.status}</div></div>
                  </div>
                {/each}
              </div>
            {/if}
          </div>

        {:else if currentStep === 8}
          <!-- CUTOVER with Readiness Checklist -->
          <CutoverChecklist
            {replicationLag}
            {healthScore}
            {replicationStatus}
            {queueStates}
            {containerHealth}
            {rollbackAvailable}
            {actionLoading}
            onCutover={() => openConfirmation('cutover')}
          />

        {:else if currentStep === 9}
          <!-- OBSERVATION -->
          <ObservationPanel
            {healthScore}
            {replicationLag}
            {observationElapsed}
            {observationDuration}
            {observationRemaining}
            {observationProgress}
            stepStatus={stepStatuses[9]}
            {actionLoading}
            onCommit={commitMigration}
            onRollback={() => openConfirmation('rollback')}
          />
        {/if}

        <!-- Bottom Tabs -->
        <BottomTabs
          {auditTrail}
          {wsMessages}
          events={migrationEvents}
          {warnings}
          {dependencyGraph}
          {riskReport}
          onRefreshAudit={refreshAudit}
          onExportReport={exportReport}
        />

      {:else if currentStep === 10}
        <!-- FINISH -->
        <div class="bg-surface rounded-xl p-4 sm:p-8 border border-border text-center mb-4">
          <div class="w-16 h-16 rounded-full bg-success/10 flex items-center justify-center mx-auto mb-4">
            <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-success"><path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/><polyline points="22 4 12 14.01 9 11.01"/></svg>
          </div>
          <h2 class="text-2xl font-bold mb-2">Migration Complete</h2>
          <p class="text-fg-subtle mb-6">Migration #{migrationId} has been successfully completed.</p>
          <div class="grid grid-cols-2 sm:grid-cols-4 gap-4 mb-6">
            <div class="bg-surface-muted rounded-lg p-4"><div class="text-xs text-fg-subtle">Risk Score</div><div class="text-xl font-bold {riskReport ? riskColor(riskReport.riskClass) : ''}">{riskReport?.riskScore.toFixed(0) || '—'}</div></div>
            <div class="bg-surface-muted rounded-lg p-4"><div class="text-xs text-fg-subtle">Downtime</div><div class="text-xl font-bold">{riskReport?.downtimeEstimate || '—'}</div></div>
            <div class="bg-surface-muted rounded-lg p-4"><div class="text-xs text-fg-subtle">Health Score</div><div class="text-xl font-bold text-success">{healthScore.toFixed(0)}</div></div>
            <div class="bg-surface-muted rounded-lg p-4"><div class="text-xs text-fg-subtle">Categories</div><div class="text-xl font-bold">{config.categories.length}</div></div>
          </div>
          <div class="flex items-center justify-center gap-4">
            <button on:click={exportReport} class="flex items-center gap-2 px-6 py-2.5 bg-accent hover:bg-accent-hover text-accent-fg rounded-lg font-medium transition-colors">Export Report</button>
            <a href="/migrations" class="px-6 py-2.5 bg-surface-muted hover:bg-surface rounded-lg font-medium transition-colors">Back to Migrations</a>
          </div>
        </div>
        <BottomTabs {auditTrail} {wsMessages} events={migrationEvents} {warnings} {dependencyGraph} {riskReport} onRefreshAudit={refreshAudit} onExportReport={exportReport} />
      {/if}
    </div>

    <!-- Right Panel: Live Monitor (hidden on mobile, shown on desktop) -->
    <div class="hidden lg:block">
      <LiveMonitor {sourceId} {targetId} {pipelineRunning} />
    </div>
  </div>
</div>

{#if confirmationAction}
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/70 px-4" role="presentation">
    <div class="w-full max-w-md rounded-xl border border-border-strong bg-surface p-6 shadow-2xl" role="dialog" aria-modal="true" tabindex="-1">
      <div class="flex items-start gap-3">
        <div class="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-lg {confirmationAction === 'rollback' ? 'bg-error/15 text-error' : 'bg-warning/15 text-warning'}">
          {#if confirmationAction === 'rollback'}
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/></svg>
          {:else}
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M13 2 3 14h9l-1 8 10-12h-9l1-8z"/></svg>
          {/if}
        </div>
        <div>
          <h2 class="text-lg font-semibold">{confirmationAction === 'rollback' ? 'Confirm Rollback' : 'Confirm Cutover'}</h2>
          <p class="mt-2 text-sm leading-6 text-fg-muted">
            {confirmationAction === 'rollback'
              ? 'This will revert migration changes and restore the last known rollback point. Continue only if you are ready to interrupt the current migration flow.'
              : 'This will freeze writes, run the final sync, verify health, and switch traffic from source to target. Continue only when replication and health checks are ready.'}
          </p>
        </div>
      </div>
      <div class="mt-6 flex justify-end gap-3">
        <button type="button" on:click={closeConfirmation} disabled={actionLoading} class="rounded-lg bg-surface-muted px-4 py-2 text-sm font-medium text-fg-muted transition-colors hover:bg-surface disabled:cursor-not-allowed disabled:opacity-50">Cancel</button>
        <button type="button" on:click={runConfirmedAction} disabled={actionLoading} class="rounded-lg px-4 py-2 text-sm font-medium text-accent-fg transition-colors disabled:cursor-not-allowed disabled:opacity-50 {confirmationAction === 'rollback' ? 'bg-error hover:bg-error/90' : 'bg-warning hover:bg-warning/90'}">
          {actionLoading ? 'Working...' : confirmationAction === 'rollback' ? 'Rollback' : 'Cutover'}
        </button>
      </div>
    </div>
  </div>
{/if}
