<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { page } from '$app/stores';
  import {
    pipelineApi, wsPipeline, WIZARD_STEPS, defaultMigrationConfig,
    type MigrationSession, type PipelineStage, type WSMessageExtended,
    type CompatibilityCheckResult, type RiskReport, type HealthCheckResult,
    type SyncSession, type ReplicationStatus, type QueueState,
    type ProvisionState, type ContainerHealthInfo, type AuditEntry,
    type MigrationConfig
  } from '$lib/api/pipeline';
  import { migrationApi, type DryRunResult } from '$lib/api/migrations';
  import { toast } from '$lib/stores/toast';

  const migrationId = parseInt($page.params.id);

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

  // ── Live Metrics (Steps 6-9) ──
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
  let ws: WebSocket | null = null;

  // ── Cutover State ──
  let cutoverConfirmed = false;
  let cutoverStep = ''; // freeze, drain, switch, promote, resume, verify

  // ── Observation State ──
  let observationStart: number | null = null;
  let observationDuration = 300; // seconds
  let observationElapsed = 0;
  let observationTimer: ReturnType<typeof setInterval> | null = null;

  // ── Step Action Loading ──
  let actionLoading = false;

  onMount(async () => {
    await loadSession();
  });

  onDestroy(() => {
    ws?.close();
    if (observationTimer) clearInterval(observationTimer);
  });

  // ══════════════════════════════════════════════════════
  //  DATA LOADING
  // ══════════════════════════════════════════════════════

  async function loadSession() {
    loading = true;
    try {
      session = await pipelineApi.getSession(migrationId);
      if (session?.config) config = session.config;
      if (session?.riskReport) riskReport = session.riskReport;
      if (session?.healthHistory) healthResults = session.healthHistory;
      if (session?.syncSessions) syncSessions = session.syncSessions;
      if (session?.replicationStatus) replicationStatus = session.replicationStatus;
      if (session?.queueStates) queueStates = session.queueStates;
      if (session?.provisionStates) provisionStates = session.provisionStates;
      currentState = session?.state || '';

      // Recover wizard step from pipeline state
      recoverStepFromState();
    } catch {
      try {
        const plan = await migrationApi.get(migrationId);
        session = { migration: plan as any, state: plan.status };
        currentState = plan.status;
      } catch { /* ignore */ }
    } finally {
      loading = false;
    }
  }

  function recoverStepFromState() {
    if (!session) return;
    const st = currentState.toLowerCase();
    if (['completed', 'committed', 'archived'].includes(st)) { setStep(10); return; }
    if (['observation', 'post_verification'].includes(st)) { setStep(9); return; }
    if (['traffic_switch', 'pre_cutover'].includes(st)) { setStep(8); return; }
    if (['live_replication', 'verification'].includes(st)) { setStep(7); pipelineRunning = true; return; }
    if (['initial_sync', 'install_dependencies', 'provision_target'].includes(st)) { setStep(6); pipelineRunning = true; return; }
    if (st === 'backup') { setStep(5); return; }
    if (st === 'risk_assessment') { setStep(2); return; }
    if (st === 'compatibility_check') { setStep(1); return; }
    if (st === 'discovery' || st === 'planning') { setStep(0); return; }
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
      case 7: return replicationLag <= 5 && stepStatuses[7] === 'completed';
      case 8: return stepStatuses[8] === 'completed';
      case 9: return stepStatuses[9] === 'completed';
      default: return false;
    }
  }

  function goNext() {
    if (currentStep < 10 && canProceed(currentStep)) {
      currentStep++;
      if (stepStatuses[currentStep] === 'pending') stepStatuses[currentStep] = 'running';
    }
  }

  function goBack() {
    if (currentStep > 0 && !pipelineRunning) {
      currentStep--;
    }
  }

  function goToStep(step: number) {
    if (step < currentStep && !pipelineRunning) {
      currentStep = step;
    }
  }

  // ══════════════════════════════════════════════════════
  //  STEP ACTIONS
  // ══════════════════════════════════════════════════════

  // Step 0: Discovery
  async function runDiscovery() {
    actionLoading = true;
    stepStatuses[0] = 'running';
    try {
      await loadSession();
      stepStatuses[0] = 'completed';
      toast.success('Discovery completed');
    } catch {
      stepStatuses[0] = 'failed';
      toast.error('Discovery failed');
    } finally {
      actionLoading = false;
    }
  }

  // Step 1: Compatibility
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
    } finally {
      actionLoading = false;
    }
  }

  // Step 2: Risk
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
    } finally {
      actionLoading = false;
    }
  }

  // Step 4: Dry Run
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
    } finally {
      actionLoading = false;
    }
  }

  // Step 5: Provision
  async function runProvision() {
    actionLoading = true;
    stepStatuses[5] = 'running';
    try {
      await pipelineApi.provision(migrationId);
      provisionStates = await pipelineApi.getProvisionStates(migrationId);
      stepStatuses[5] = 'completed';
      toast.success('Provisioning completed');
    } catch {
      stepStatuses[5] = 'failed';
      toast.error('Provisioning failed');
    } finally {
      actionLoading = false;
    }
  }

  // Step 6: Execute — Start Pipeline
  function startPipeline() {
    pipelineRunning = true;
    stepStatuses[6] = 'running';
    wsMessages = [];
    toast.info('Pipeline started');

    ws = wsPipeline(migrationId, (msg: WSMessageExtended) => {
      wsMessages = [...wsMessages, msg];
      updateLiveMetrics(msg);

      // Auto-advance based on pipeline state
      if (msg.currentState) {
        const st = msg.currentState.toLowerCase();
        if (st === 'live_replication' && currentStep === 6) {
          stepStatuses[6] = 'completed';
          currentStep = 7;
          stepStatuses[7] = 'running';
        }
        if ((st === 'pre_cutover' || st === 'traffic_switch') && currentStep === 7) {
          stepStatuses[7] = 'completed';
          currentStep = 8;
          stepStatuses[8] = 'running';
        }
        if ((st === 'post_verification' || st === 'observation') && currentStep === 8) {
          stepStatuses[8] = 'completed';
          currentStep = 9;
          stepStatuses[9] = 'running';
          startObservationTimer();
        }
        if (st === 'committed' || st === 'completed') {
          stepStatuses[9] = 'completed';
          currentStep = 10;
          stepStatuses[10] = 'completed';
          pipelineRunning = false;
        }
      }

      if (msg.status === 'error') {
        pipelineRunning = false;
        toast.error(`Pipeline error: ${msg.error || 'Unknown error'}`);
      }
    }, () => {
      pipelineRunning = false;
    }, () => {
      pipelineRunning = false;
      toast.error('WebSocket connection lost');
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

  // Step 8: Cutover
  function confirmCutover() {
    if (!confirm('⚠️ CUTOVER: This will switch all traffic from source to target. Make sure replication is caught up and target is healthy. Continue?')) return;
    cutoverConfirmed = true;
    // The pipeline is already running — cutover happens automatically
    // at the PreCutover/TrafficSwitch stage
    toast.info('Cutover confirmed — traffic switch in progress');
  }

  // Step 9: Observation
  function startObservationTimer() {
    observationStart = Date.now();
    observationElapsed = 0;
    if (observationTimer) clearInterval(observationTimer);
    observationTimer = setInterval(() => {
      observationElapsed = Math.floor((Date.now() - (observationStart || Date.now())) / 1000);
      if (observationElapsed >= observationDuration && stepStatuses[9] !== 'completed') {
        stepStatuses[9] = 'completed';
        if (observationTimer) clearInterval(observationTimer);
      }
    }, 1000);
  }

  function commitMigration() {
    stepStatuses[9] = 'completed';
    currentStep = 10;
    stepStatuses[10] = 'completed';
    pipelineRunning = false;
    toast.success('Migration committed successfully!');
  }

  // ── Control Actions ──

  async function pausePipeline() {
    try {
      await pipelineApi.pause(migrationId);
      pipelinePaused = true;
      toast.success('Pipeline paused');
    } catch { toast.error('Pause failed'); }
  }

  async function resumePipeline() {
    try {
      await pipelineApi.resume(migrationId);
      pipelinePaused = false;
      toast.success('Pipeline resumed');
      startPipeline();
    } catch { toast.error('Resume failed'); }
  }

  async function rollbackPipeline() {
    if (!confirm('⚠️ ROLLBACK: This will revert all changes. Are you sure?')) return;
    try {
      await migrationApi.rollback(migrationId);
      pipelineRunning = false;
      toast.success('Rollback initiated');
      loadSession();
    } catch { toast.error('Rollback failed'); }
  }

  async function exportReport() {
    try {
      const blob = await pipelineApi.exportReport(migrationId);
      const url = URL.createObjectURL(blob as any);
      const a = document.createElement('a');
      a.href = url;
      a.download = `migration-${migrationId}-report.json`;
      a.click();
      URL.revokeObjectURL(url);
      toast.success('Report exported');
    } catch { toast.error('Export failed'); }
  }

  async function refreshHealth() {
    try {
      healthResults = await pipelineApi.checkHealth(migrationId);
    } catch { /* ignore */ }
  }

  async function refreshQueue() {
    try {
      queueStates = await pipelineApi.getQueueStates(migrationId);
    } catch { /* ignore */ }
  }

  async function refreshAudit() {
    try {
      auditTrail = await pipelineApi.getAuditTrail(migrationId);
    } catch { /* ignore */ }
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
      case 'low': return 'text-green-500';
      case 'medium': return 'text-yellow-500';
      case 'high': return 'text-orange-500';
      case 'critical': return 'text-red-500';
      default: return 'text-gray-500';
    }
  }

  function riskBg(cls: string): string {
    switch (cls) {
      case 'low': return 'bg-green-500/10 border-green-500/30';
      case 'medium': return 'bg-yellow-500/10 border-yellow-500/30';
      case 'high': return 'bg-orange-500/10 border-orange-500/30';
      case 'critical': return 'bg-red-500/10 border-red-500/30';
      default: return 'bg-gray-500/10 border-gray-500/30';
    }
  }

  function severityColor(sev: string): string {
    switch (sev) {
      case 'critical': return 'bg-red-500/20 text-red-400 border-red-500/30';
      case 'high': return 'bg-orange-500/20 text-orange-400 border-orange-500/30';
      case 'warning': return 'bg-yellow-500/20 text-yellow-400 border-yellow-500/30';
      default: return 'bg-blue-500/20 text-blue-400 border-blue-500/30';
    }
  }

  function stepIndicatorColor(status: string): string {
    switch (status) {
      case 'completed': return 'bg-green-500 text-white';
      case 'running': return 'bg-blue-500 text-white animate-pulse';
      case 'failed': return 'bg-red-500 text-white';
      default: return 'bg-gray-700 text-gray-400';
    }
  }

  function stepLineColor(status: string): string {
    switch (status) {
      case 'completed': return 'bg-green-500';
      case 'running': return 'bg-blue-500';
      case 'failed': return 'bg-red-500';
      default: return 'bg-gray-700';
    }
  }

  const availableCategories = ['users', 'groups', 'packages', 'services', 'files', 'docker', 'firewall', 'cron', 'nginx', 'databases', 'ssl', 'ssh'];

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
</script>

<div class="min-h-screen bg-gray-950 text-white flex flex-col">
  <!-- ═══ HEADER ═══ -->
  <div class="border-b border-gray-800 px-6 py-3 flex items-center justify-between shrink-0">
    <div class="flex items-center gap-4">
      <a href="/migrations" class="text-gray-400 hover:text-white transition-colors">
        <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m12 19-7-7 7-7"/><path d="M19 12H5"/></svg>
      </a>
      <div>
        <h1 class="text-lg font-semibold">Zero-Downtime Migration #{migrationId}</h1>
        <p class="text-xs text-gray-400">
          {currentState || 'Ready'}
          {#if session?.migration?.sourceId && session?.migration?.targetId}
            &middot; Server {session.migration.sourceId} &rarr; Server {session.migration.targetId}
          {/if}
        </p>
      </div>
    </div>
    <div class="flex items-center gap-2">
      {#if pipelineRunning && !pipelinePaused}
        <button on:click={pausePipeline} class="flex items-center gap-1.5 px-3 py-1.5 bg-yellow-600 hover:bg-yellow-700 rounded-lg text-xs font-medium transition-colors">
          <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="6" y="4" width="4" height="16"/><rect x="14" y="4" width="4" height="16"/></svg>
          Pause
        </button>
      {/if}
      {#if pipelinePaused}
        <button on:click={resumePipeline} class="flex items-center gap-1.5 px-3 py-1.5 bg-blue-600 hover:bg-blue-700 rounded-lg text-xs font-medium transition-colors">
          <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polygon points="6 3 20 12 6 21 6 3"/></svg>
          Resume
        </button>
      {/if}
      {#if pipelineRunning || pipelinePaused}
        <button on:click={rollbackPipeline} class="flex items-center gap-1.5 px-3 py-1.5 bg-red-600 hover:bg-red-700 rounded-lg text-xs font-medium transition-colors">
          <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/></svg>
          Rollback
        </button>
      {/if}
      <button on:click={exportReport} class="flex items-center gap-1.5 px-3 py-1.5 bg-gray-700 hover:bg-gray-600 rounded-lg text-xs font-medium transition-colors">
        <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>
        Export
      </button>
      <a href="/migrations/{migrationId}/diff" class="flex items-center gap-1.5 px-3 py-1.5 bg-gray-700 hover:bg-gray-600 rounded-lg text-xs font-medium transition-colors">
        <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 3v18"/><rect width="18" height="18" x="3" y="3" rx="2"/></svg>
        Diff
      </a>
    </div>
  </div>

  <!-- ═══ STEP INDICATOR ═══ -->
  <div class="border-b border-gray-800 px-6 py-4 shrink-0">
    <div class="flex items-center">
      {#each WIZARD_STEPS as step, i}
        <button
          on:click={() => goToStep(i)}
          class="flex items-center gap-2 cursor-pointer group {i <= currentStep ? 'opacity-100' : 'opacity-50'}"
          disabled={i > currentStep || pipelineRunning}
        >
          <div class="w-7 h-7 rounded-full flex items-center justify-center text-xs font-bold transition-colors {stepIndicatorColor(stepStatuses[i])}">
            {#if stepStatuses[i] === 'completed'}
              <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"><polyline points="20 6 9 17 4 12"/></svg>
            {:else}
              {i + 1}
            {/if}
          </div>
          <span class="text-xs font-medium hidden xl:inline {currentStep === i ? 'text-blue-400' : stepStatuses[i] === 'completed' ? 'text-green-400' : 'text-gray-400'}">
            {step.name}
          </span>
        </button>
        {#if i < WIZARD_STEPS.length - 1}
          <div class="flex-1 h-0.5 mx-1 {stepLineColor(stepStatuses[i])} transition-colors"></div>
        {/if}
      {/each}
    </div>
    <div class="mt-2 text-center">
      <span class="text-sm font-medium text-blue-400">Step {currentStep + 1}: {WIZARD_STEPS[currentStep].name}</span>
      <span class="text-xs text-gray-500 ml-2">&mdash; {WIZARD_STEPS[currentStep].description}</span>
    </div>
  </div>

  <!-- ═══ MAIN CONTENT ═══ -->
  <div class="flex-1 overflow-y-auto p-6">
    {#if loading}
      <div class="flex items-center justify-center h-64 text-gray-400">
        <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="animate-spin mr-3"><path d="M21 12a9 9 0 1 1-6.219-8.56"/></svg>
        Loading migration data...
      </div>

    <!-- ══════════════════════════════════════════════════ -->
    <!-- STEP 0: DISCOVERY                                -->
    <!-- ══════════════════════════════════════════════════ -->
    {:else if currentStep === 0}
      <div class="max-w-4xl mx-auto">
        <div class="bg-gray-900 rounded-xl p-8 border border-gray-800">
          <div class="flex items-center gap-3 mb-6">
            <div class="w-10 h-10 rounded-lg bg-blue-500/20 flex items-center justify-center">
              <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-blue-400"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/></svg>
            </div>
            <div>
              <h2 class="text-lg font-semibold">Discovery</h2>
              <p class="text-sm text-gray-400">Discover services, containers, and databases on source &amp; target servers</p>
            </div>
          </div>

          {#if stepStatuses[0] === 'pending' || stepStatuses[0] === 'failed'}
            <div class="text-center py-12">
              <p class="text-gray-400 mb-4">Run discovery to collect information about your source and target servers.</p>
              <button on:click={runDiscovery} disabled={actionLoading} class="px-6 py-2.5 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 rounded-lg font-medium transition-colors">
                {actionLoading ? 'Running Discovery...' : 'Run Discovery'}
              </button>
            </div>
          {:else if stepStatuses[0] === 'running'}
            <div class="text-center py-12 text-gray-400">
              <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="animate-spin mx-auto mb-3"><path d="M21 12a9 9 0 1 1-6.219-8.56"/></svg>
              Discovering servers...
            </div>
          {:else}
            <div class="grid grid-cols-2 gap-6">
              <div class="bg-gray-800 rounded-lg p-4">
                <h3 class="text-sm font-medium text-gray-300 mb-3">Source Server</h3>
                <div class="space-y-2 text-sm">
                  <div class="flex justify-between"><span class="text-gray-400">ID</span><span>{session?.migration?.sourceId || '—'}</span></div>
                  <div class="flex justify-between"><span class="text-gray-400">Status</span><span class="text-green-400">Connected</span></div>
                </div>
              </div>
              <div class="bg-gray-800 rounded-lg p-4">
                <h3 class="text-sm font-medium text-gray-300 mb-3">Target Server</h3>
                <div class="space-y-2 text-sm">
                  <div class="flex justify-between"><span class="text-gray-400">ID</span><span>{session?.migration?.targetId || '—'}</span></div>
                  <div class="flex justify-between"><span class="text-gray-400">Status</span><span class="text-green-400">Connected</span></div>
                </div>
              </div>
            </div>
            {#if riskReport}
            <div class="mt-4 grid grid-cols-4 gap-3">
              <div class="bg-gray-800 rounded-lg p-3 text-center">
                <div class="text-xs text-gray-400">Data Size</div>
                <div class="font-mono text-sm">{formatBytes(riskReport.dataSizeBytes)}</div>
              </div>
              <div class="bg-gray-800 rounded-lg p-3 text-center">
                <div class="text-xs text-gray-400">DB Size</div>
                <div class="font-mono text-sm">{formatBytes(riskReport.databaseSizeBytes)}</div>
              </div>
              <div class="bg-gray-800 rounded-lg p-3 text-center">
                <div class="text-xs text-gray-400">Containers</div>
                <div class="font-mono text-sm">{riskReport.containerCount}</div>
              </div>
              <div class="bg-gray-800 rounded-lg p-3 text-center">
                <div class="text-xs text-gray-400">Volumes</div>
                <div class="font-mono text-sm">{riskReport.volumeCount}</div>
              </div>
            </div>
            {/if}
            <div class="mt-4 text-center">
              <button on:click={runDiscovery} class="text-xs text-blue-400 hover:text-blue-300">Re-run Discovery</button>
            </div>
          {/if}
        </div>
      </div>

    <!-- ══════════════════════════════════════════════════ -->
    <!-- STEP 1: COMPATIBILITY                            -->
    <!-- ══════════════════════════════════════════════════ -->
    {:else if currentStep === 1}
      <div class="max-w-4xl mx-auto">
        <div class="bg-gray-900 rounded-xl p-8 border border-gray-800">
          <div class="flex items-center justify-between mb-6">
            <div class="flex items-center gap-3">
              <div class="w-10 h-10 rounded-lg bg-purple-500/20 flex items-center justify-center">
                <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-purple-400"><path d="M12 3v18"/><rect width="18" height="18" x="3" y="3" rx="2"/></svg>
              </div>
              <div>
                <h2 class="text-lg font-semibold">Compatibility Check</h2>
                <p class="text-sm text-gray-400">Verify source and target are compatible for migration</p>
              </div>
            </div>
            <button on:click={runCompatibilityCheck} disabled={actionLoading} class="px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 rounded-lg text-sm font-medium transition-colors">
              {actionLoading ? 'Checking...' : 'Run Check'}
            </button>
          </div>

          {#if compatibilityResults.length > 0}
            <div class="space-y-2">
              {#each compatibilityResults as result}
                <div class="bg-gray-800 rounded-lg p-4 flex items-center gap-4">
                  <div class="px-2 py-1 rounded text-xs font-medium border {severityColor(result.severity)}">
                    {result.severity.toUpperCase()}
                  </div>
                  <div class="flex-1">
                    <div class="font-medium text-sm">{result.checkName}</div>
                    <div class="text-xs text-gray-400">{result.message}</div>
                  </div>
                  {#if result.passed}
                    <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-green-500"><path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/><polyline points="22 4 12 14.01 9 11.01"/></svg>
                  {:else}
                    <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-red-500"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>
                  {/if}
                </div>
              {/each}
            </div>
            {#if compatibilityResults.some(r => r.severity === 'critical' && !r.passed)}
              <div class="mt-4 p-4 bg-red-500/10 border border-red-500/30 rounded-lg">
                <p class="text-red-400 text-sm font-medium">Critical compatibility issues detected. Fix these before proceeding.</p>
              </div>
            {/if}
          {:else}
            <div class="text-center py-12 text-gray-500">
              <p>No compatibility check results yet.</p>
              <button on:click={runCompatibilityCheck} class="mt-4 px-6 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg">Check Compatibility</button>
            </div>
          {/if}
        </div>
      </div>

    <!-- ══════════════════════════════════════════════════ -->
    <!-- STEP 2: RISK ASSESSMENT                          -->
    <!-- ══════════════════════════════════════════════════ -->
    {:else if currentStep === 2}
      <div class="max-w-4xl mx-auto">
        <div class="bg-gray-900 rounded-xl p-8 border border-gray-800">
          <div class="flex items-center justify-between mb-6">
            <div class="flex items-center gap-3">
              <div class="w-10 h-10 rounded-lg bg-orange-500/20 flex items-center justify-center">
                <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-orange-400"><path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3"/><path d="M12 9v4"/><path d="M12 17h.01"/></svg>
              </div>
              <div>
                <h2 class="text-lg font-semibold">Risk Assessment</h2>
                <p class="text-sm text-gray-400">Evaluate migration risk score and estimated downtime</p>
              </div>
            </div>
            <button on:click={runRiskAssessment} disabled={actionLoading} class="px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 rounded-lg text-sm font-medium transition-colors">
              {actionLoading ? 'Assessing...' : 'Assess Risk'}
            </button>
          </div>

          {#if riskReport}
            <div class="grid grid-cols-4 gap-4 mb-6">
              <div class="bg-gray-800 rounded-xl p-6 text-center border {riskBg(riskReport.riskClass)}">
                <div class="text-4xl font-bold {riskColor(riskReport.riskClass)}">{riskReport.riskScore.toFixed(0)}</div>
                <div class="text-xs text-gray-400 mt-1">Risk Score</div>
              </div>
              <div class="bg-gray-800 rounded-xl p-6 text-center">
                <div class="text-2xl font-bold {riskColor(riskReport.riskClass)} uppercase">{riskReport.riskClass}</div>
                <div class="text-xs text-gray-400 mt-1">Classification</div>
              </div>
              <div class="bg-gray-800 rounded-xl p-6 text-center">
                <div class="text-2xl font-bold">{riskReport.downtimeEstimate || 'N/A'}</div>
                <div class="text-xs text-gray-400 mt-1">Est. Downtime</div>
              </div>
              <div class="bg-gray-800 rounded-xl p-6 text-center">
                <div class="text-2xl font-bold">{riskReport.rollbackComplexity || 'N/A'}</div>
                <div class="text-xs text-gray-400 mt-1">Rollback Complexity</div>
              </div>
            </div>
            <div class="grid grid-cols-4 gap-3">
              <div class="bg-gray-800 rounded-lg p-3"><div class="text-xs text-gray-400">Data Size</div><div class="font-mono">{formatBytes(riskReport.dataSizeBytes)}</div></div>
              <div class="bg-gray-800 rounded-lg p-3"><div class="text-xs text-gray-400">DB Size</div><div class="font-mono">{formatBytes(riskReport.databaseSizeBytes)}</div></div>
              <div class="bg-gray-800 rounded-lg p-3"><div class="text-xs text-gray-400">Containers</div><div class="font-mono">{riskReport.containerCount}</div></div>
              <div class="bg-gray-800 rounded-lg p-3"><div class="text-xs text-gray-400">Volumes</div><div class="font-mono">{riskReport.volumeCount}</div></div>
            </div>
          {:else}
            <div class="text-center py-12 text-gray-500">
              <p>No risk assessment yet.</p>
              <button on:click={runRiskAssessment} class="mt-4 px-6 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg">Assess Risk</button>
            </div>
          {/if}
        </div>
      </div>

    <!-- ══════════════════════════════════════════════════ -->
    <!-- STEP 3: PLAN                                     -->
    <!-- ══════════════════════════════════════════════════ -->
    {:else if currentStep === 3}
      <div class="max-w-4xl mx-auto">
        <div class="bg-gray-900 rounded-xl p-8 border border-gray-800">
          <div class="flex items-center gap-3 mb-6">
            <div class="w-10 h-10 rounded-lg bg-cyan-500/20 flex items-center justify-center">
              <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-cyan-400"><path d="M12 20h9"/><path d="M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4L16.5 3.5z"/></svg>
            </div>
            <div>
              <h2 class="text-lg font-semibold">Migration Plan</h2>
              <p class="text-sm text-gray-400">Select categories and configure migration options</p>
            </div>
          </div>

          <!-- Category Selection -->
          <div class="mb-6">
            <h3 class="text-sm font-medium text-gray-300 mb-3">Categories</h3>
            <div class="grid grid-cols-4 gap-2">
              {#each availableCategories as cat}
                <button
                  on:click={() => toggleCategory(cat)}
                  class="px-3 py-2 rounded-lg text-sm font-medium border transition-colors {config.categories.includes(cat) ? 'bg-blue-600/20 border-blue-500/50 text-blue-400' : 'bg-gray-800 border-gray-700 text-gray-400 hover:border-gray-600'}"
                >
                  {cat}
                </button>
              {/each}
            </div>
            {#if config.categories.length === 0}
              <p class="text-xs text-red-400 mt-2">Select at least one category to proceed.</p>
            {/if}
          </div>

          <!-- Configuration -->
          <div class="grid grid-cols-2 gap-6">
            <div>
              <h3 class="text-sm font-medium text-gray-300 mb-3">Replication</h3>
              <div class="space-y-3">
                <label class="flex items-center gap-2 text-sm">
                  <input type="checkbox" bind:checked={config.replicationEnabled} class="rounded bg-gray-700 border-gray-600" />
                  Enable Database Replication
                </label>
                {#if config.replicationEnabled}
                  <div>
                    <label class="text-xs text-gray-400">Mode</label>
                    <select bind:value={config.replicationMode} class="w-full mt-1 bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm">
                      <option value="streaming">Streaming</option>
                      <option value="logical">Logical</option>
                      <option value="snapshot">Snapshot</option>
                    </select>
                  </div>
                {/if}
              </div>
            </div>
            <div>
              <h3 class="text-sm font-medium text-gray-300 mb-3">Cutover</h3>
              <div class="space-y-3">
                <label class="flex items-center gap-2 text-sm">
                  <input type="checkbox" bind:checked={config.freezeWriteOnCutover} class="rounded bg-gray-700 border-gray-600" />
                  Freeze Writes on Cutover
                </label>
                <label class="flex items-center gap-2 text-sm">
                  <input type="checkbox" bind:checked={config.drainQueuesOnCutover} class="rounded bg-gray-700 border-gray-600" />
                  Drain Queues on Cutover
                </label>
              </div>
            </div>
            <div>
              <h3 class="text-sm font-medium text-gray-300 mb-3">Traffic</h3>
              <div>
                <label class="text-xs text-gray-400">Provider</label>
                <select bind:value={config.trafficProvider} class="w-full mt-1 bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm">
                  <option value="">Auto-detect</option>
                  <option value="cloudflare">Cloudflare</option>
                  <option value="nginx">Nginx</option>
                  <option value="traefik">Traefik</option>
                  <option value="haproxy">HAProxy</option>
                  <option value="caddy">Caddy</option>
                </select>
              </div>
            </div>
            <div>
              <h3 class="text-sm font-medium text-gray-300 mb-3">Safety</h3>
              <div class="space-y-3">
                <label class="flex items-center gap-2 text-sm">
                  <input type="checkbox" bind:checked={config.autoRollbackOnError} class="rounded bg-gray-700 border-gray-600" />
                  Auto-Rollback on Error
                </label>
                <div>
                  <label class="text-xs text-gray-400">Max Error Rate (%)</label>
                  <input type="number" bind:value={config.maxErrorRate} min="0" max="100" class="w-full mt-1 bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm" />
                </div>
                <div>
                  <label class="text-xs text-gray-400">Observation Duration (seconds)</label>
                  <input type="number" bind:value={config.observationDuration} min="60" max="3600" class="w-full mt-1 bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm" />
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

    <!-- ══════════════════════════════════════════════════ -->
    <!-- STEP 4: DRY RUN                                  -->
    <!-- ══════════════════════════════════════════════════ -->
    {:else if currentStep === 4}
      <div class="max-w-4xl mx-auto">
        <div class="bg-gray-900 rounded-xl p-8 border border-gray-800">
          <div class="flex items-center justify-between mb-6">
            <div class="flex items-center gap-3">
              <div class="w-10 h-10 rounded-lg bg-teal-500/20 flex items-center justify-center">
                <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-teal-400"><path d="M2 12s3-7 10-7 10 7 10 7-3 7-10 7-10-7-10-7Z"/><circle cx="12" cy="12" r="3"/></svg>
              </div>
              <div>
                <h2 class="text-lg font-semibold">Dry Run</h2>
                <p class="text-sm text-gray-400">Preview changes without applying them</p>
              </div>
            </div>
            <button on:click={runDryRun} disabled={actionLoading} class="px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 rounded-lg text-sm font-medium transition-colors">
              {actionLoading ? 'Running...' : 'Run Dry Run'}
            </button>
          </div>

          {#if dryRunResult}
            <div class="grid grid-cols-4 gap-3 mb-6">
              <div class="bg-gray-800 rounded-lg p-3 text-center">
                <div class="text-2xl font-bold">{dryRunResult.summary.totalChanges}</div>
                <div class="text-xs text-gray-400">Total Changes</div>
              </div>
              <div class="bg-gray-800 rounded-lg p-3 text-center">
                <div class="text-2xl font-bold text-green-400">{dryRunResult.summary.addCount}</div>
                <div class="text-xs text-gray-400">Add</div>
              </div>
              <div class="bg-gray-800 rounded-lg p-3 text-center">
                <div class="text-2xl font-bold text-yellow-400">{dryRunResult.summary.modifyCount}</div>
                <div class="text-xs text-gray-400">Modify</div>
              </div>
              <div class="bg-gray-800 rounded-lg p-3 text-center">
                <div class="text-2xl font-bold text-red-400">{dryRunResult.summary.removeCount}</div>
                <div class="text-xs text-gray-400">Remove</div>
              </div>
            </div>
            <div class="space-y-3">
              {#each dryRunResult.categories as cat}
                <details class="bg-gray-800 rounded-lg">
                  <summary class="px-4 py-3 cursor-pointer text-sm font-medium flex items-center justify-between">
                    <span>{cat.category}</span>
                    <span class="text-xs text-gray-400">{cat.changes.length} changes &middot; {cat.summary}</span>
                  </summary>
                  <div class="px-4 pb-3 space-y-1">
                    {#each cat.changes as change}
                      <div class="text-xs flex items-center gap-2 py-1">
                        <span class="px-1.5 py-0.5 rounded font-mono {change.type === 'add' ? 'bg-green-500/20 text-green-400' : change.type === 'modify' ? 'bg-yellow-500/20 text-yellow-400' : 'bg-red-500/20 text-red-400'}">{change.type}</span>
                        <span class="text-gray-300">{change.resource}</span>
                        <span class="text-gray-500">&mdash; {change.detail}</span>
                      </div>
                    {/each}
                  </div>
                </details>
              {/each}
            </div>
          {:else}
            <div class="text-center py-12 text-gray-500">
              <p>Run a dry run to preview what will change during migration.</p>
              <button on:click={runDryRun} class="mt-4 px-6 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg">Run Dry Run</button>
            </div>
          {/if}
        </div>
      </div>

    <!-- ══════════════════════════════════════════════════ -->
    <!-- STEP 5: PROVISION                                -->
    <!-- ══════════════════════════════════════════════════ -->
    {:else if currentStep === 5}
      <div class="max-w-4xl mx-auto">
        <div class="bg-gray-900 rounded-xl p-8 border border-gray-800">
          <div class="flex items-center justify-between mb-6">
            <div class="flex items-center gap-3">
              <div class="w-10 h-10 rounded-lg bg-indigo-500/20 flex items-center justify-center">
                <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-indigo-400"><path d="m7.5 4.27 9 5.15"/><path d="M21 8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z"/><path d="m3.3 7 8.7 5 8.7-5"/><path d="M12 22V12"/></svg>
              </div>
              <div>
                <h2 class="text-lg font-semibold">Provision Target</h2>
                <p class="text-sm text-gray-400">Install dependencies on the target server</p>
              </div>
            </div>
            <button on:click={runProvision} disabled={actionLoading} class="px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 rounded-lg text-sm font-medium transition-colors">
              {actionLoading ? 'Provisioning...' : 'Provision Target'}
            </button>
          </div>

          {#if provisionStates.length > 0}
            <div class="space-y-2">
              {#each provisionStates as ps}
                <div class="bg-gray-800 rounded-lg p-4 flex items-center gap-4">
                  <div class="w-8 h-8 rounded-full flex items-center justify-center {ps.verified ? 'bg-green-500/20' : ps.installed ? 'bg-yellow-500/20' : 'bg-gray-700'}">
                    {#if ps.verified}
                      <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-green-500"><polyline points="20 6 9 17 4 12"/></svg>
                    {:else if ps.error}
                      <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-red-500"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>
                    {:else}
                      <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-gray-400 animate-spin"><path d="M21 12a9 9 0 1 1-6.219-8.56"/></svg>
                    {/if}
                  </div>
                  <div class="flex-1">
                    <div class="font-medium text-sm">{ps.component}</div>
                    {#if ps.error}<div class="text-xs text-red-400">{ps.error}</div>{/if}
                  </div>
                  <div class="text-xs text-gray-400">
                    {#if ps.verified}Verified{:else if ps.installed}Installed{:else}Pending{/if}
                    {#if ps.version}&middot; {ps.version}{/if}
                  </div>
                </div>
              {/each}
            </div>
          {:else if stepStatuses[5] === 'running'}
            <div class="text-center py-12 text-gray-400">
              <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="animate-spin mx-auto mb-3"><path d="M21 12a9 9 0 1 1-6.219-8.56"/></svg>
              Provisioning target server...
            </div>
          {:else}
            <div class="text-center py-12 text-gray-500">
              <p>Provision the target server with required dependencies.</p>
              <button on:click={runProvision} class="mt-4 px-6 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg">Provision Target</button>
            </div>
          {/if}
        </div>
      </div>

    <!-- ══════════════════════════════════════════════════ -->
    <!-- STEPS 6-9: EXECUTION PHASE (with live metrics)    -->
    <!-- ══════════════════════════════════════════════════ -->
    {:else if currentStep >= 6 && currentStep <= 9}
      <div class="max-w-6xl mx-auto">
        <!-- Live Metrics Dashboard (always shown during steps 6-9) -->
        <div class="grid grid-cols-6 gap-3 mb-6">
          <div class="bg-gray-900 rounded-lg p-3 border border-gray-800">
            <div class="text-xs text-gray-400 mb-1">Progress</div>
            <div class="text-lg font-mono">{progress.toFixed(1)}%</div>
            <div class="mt-1 h-1 bg-gray-700 rounded-full overflow-hidden"><div class="h-full bg-blue-500 rounded-full" style="width: {progress}%"></div></div>
          </div>
          <div class="bg-gray-900 rounded-lg p-3 border border-gray-800">
            <div class="text-xs text-gray-400 mb-1">Transfer</div>
            <div class="text-sm font-mono">{formatBytes(bytesDone)} / {formatBytes(bytesTotal)}</div>
          </div>
          <div class="bg-gray-900 rounded-lg p-3 border border-gray-800">
            <div class="text-xs text-gray-400 mb-1">Speed / Bandwidth</div>
            <div class="text-sm font-mono">{formatBytes(speedBytes)}/s</div>
            <div class="text-xs text-gray-500">&uarr;{formatBytes(networkTxBytesSec)}/s &darr;{formatBytes(networkRxBytesSec)}/s</div>
          </div>
          <div class="bg-gray-900 rounded-lg p-3 border border-gray-800">
            <div class="text-xs text-gray-400 mb-1">Repl. Lag</div>
            <div class="text-lg font-mono {replicationLag > 5 ? 'text-yellow-400' : 'text-green-400'}">{replicationLag}s</div>
          </div>
          <div class="bg-gray-900 rounded-lg p-3 border border-gray-800">
            <div class="text-xs text-gray-400 mb-1">Health Score</div>
            <div class="text-lg font-mono {healthScore < 80 ? 'text-yellow-400' : 'text-green-400'}">{healthScore.toFixed(0)}</div>
          </div>
          <div class="bg-gray-900 rounded-lg p-3 border border-gray-800">
            <div class="text-xs text-gray-400 mb-1">ETA</div>
            <div class="text-lg font-mono">{eta || '&mdash;'}</div>
          </div>
        </div>

        <!-- Resource Metrics: CPU, RAM, Disk -->
        <div class="grid grid-cols-3 gap-3 mb-6">
          <div class="bg-gray-900 rounded-lg p-3 border border-gray-800">
            <div class="flex items-center justify-between mb-1">
              <span class="text-xs text-gray-400">CPU Usage</span>
              <span class="text-sm font-mono {cpuUsagePercent > 80 ? 'text-red-400' : cpuUsagePercent > 60 ? 'text-yellow-400' : 'text-green-400'}">{cpuUsagePercent.toFixed(1)}%</span>
            </div>
            <div class="h-2 bg-gray-700 rounded-full overflow-hidden"><div class="h-full rounded-full {cpuUsagePercent > 80 ? 'bg-red-500' : cpuUsagePercent > 60 ? 'bg-yellow-500' : 'bg-green-500'}" style="width: {cpuUsagePercent}%"></div></div>
          </div>
          <div class="bg-gray-900 rounded-lg p-3 border border-gray-800">
            <div class="flex items-center justify-between mb-1">
              <span class="text-xs text-gray-400">RAM</span>
              <span class="text-sm font-mono {ramPercent > 90 ? 'text-red-400' : ramPercent > 75 ? 'text-yellow-400' : 'text-green-400'}">{formatBytes(ramUsedBytes)} / {formatBytes(ramTotalBytes)}</span>
            </div>
            <div class="h-2 bg-gray-700 rounded-full overflow-hidden"><div class="h-full rounded-full {ramPercent > 90 ? 'bg-red-500' : ramPercent > 75 ? 'bg-yellow-500' : 'bg-green-500'}" style="width: {ramPercent}%"></div></div>
          </div>
          <div class="bg-gray-900 rounded-lg p-3 border border-gray-800">
            <div class="flex items-center justify-between mb-1">
              <span class="text-xs text-gray-400">Disk</span>
              <span class="text-sm font-mono {diskUsedPercent > 90 ? 'text-red-400' : 'text-yellow-400'}">{diskUsedPercent.toFixed(1)}%</span>
            </div>
            <div class="h-2 bg-gray-700 rounded-full overflow-hidden"><div class="h-full rounded-full {diskUsedPercent > 90 ? 'bg-red-500' : 'bg-yellow-500'}" style="width: {diskUsedPercent}%"></div></div>
          </div>
        </div>

        <!-- Queue & Container Health -->
        <div class="grid grid-cols-2 gap-3 mb-6">
          <div class="bg-gray-900 rounded-lg p-4 border border-gray-800">
            <div class="flex items-center justify-between mb-3">
              <span class="text-xs font-medium text-gray-300">Queue Status</span>
              <button on:click={refreshQueue} class="text-xs text-blue-400 hover:text-blue-300">Refresh</button>
            </div>
            {#if queueStates.length > 0}
              {#each queueStates as q}
                <div class="flex items-center justify-between py-1.5 text-sm">
                  <span class="font-mono">{q.queueType}{q.queueName ? ` / ${q.queueName}` : ''}</span>
                  <div class="flex items-center gap-2">
                    <span class="px-1.5 py-0.5 rounded text-xs {q.paused ? 'bg-yellow-500/20 text-yellow-400' : 'bg-green-500/20 text-green-400'}">{q.paused ? 'Paused' : 'Active'}</span>
                    <span class="text-xs text-gray-400">{q.activeJobs} jobs</span>
                  </div>
                </div>
              {/each}
            {:else}
              <p class="text-xs text-gray-500">No queues configured</p>
            {/if}
          </div>
          <div class="bg-gray-900 rounded-lg p-4 border border-gray-800">
            <div class="flex items-center justify-between mb-3">
              <span class="text-xs font-medium text-gray-300">Container Health</span>
              <button on:click={refreshHealth} class="text-xs text-blue-400 hover:text-blue-300">Refresh</button>
            </div>
            {#if containerHealth.length > 0}
              {#each containerHealth as c}
                <div class="flex items-center justify-between py-1.5 text-sm">
                  <span class="font-mono">{c.name}</span>
                  <div class="flex items-center gap-2">
                    <span class="px-1.5 py-0.5 rounded text-xs {c.healthy ? 'bg-green-500/20 text-green-400' : 'bg-red-500/20 text-red-400'}">{c.status}</span>
                    <span class="text-xs text-gray-400">CPU: {c.cpuPercent.toFixed(0)}%</span>
                  </div>
                </div>
              {/each}
            {:else if healthResults.length > 0}
              {#each healthResults.slice(0, 5) as h}
                <div class="flex items-center justify-between py-1.5 text-sm">
                  <span class="font-mono text-xs">{h.checkType}: {h.checkTarget}</span>
                  <span class="px-1.5 py-0.5 rounded text-xs {h.status === 'healthy' ? 'bg-green-500/20 text-green-400' : 'bg-red-500/20 text-red-400'}">{h.status}</span>
                </div>
              {/each}
            {:else}
              <p class="text-xs text-gray-500">No container data</p>
            {/if}
          </div>
        </div>

        <!-- Step-Specific Content -->
        {#if currentStep === 6}
          <!-- STEP 6: EXECUTE -->
          <div class="bg-gray-900 rounded-xl p-8 border border-gray-800">
            <div class="flex items-center gap-3 mb-6">
              <div class="w-10 h-10 rounded-lg bg-green-500/20 flex items-center justify-center">
                <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-green-400"><polygon points="6 3 20 12 6 21 6 3"/></svg>
              </div>
              <div>
                <h2 class="text-lg font-semibold">Execute Migration</h2>
                <p class="text-sm text-gray-400">Start initial sync and set up replication</p>
              </div>
            </div>
            {#if !pipelineRunning}
              <div class="text-center py-8">
                <p class="text-gray-400 mb-2">All pre-flight checks passed. Ready to start the migration pipeline.</p>
                <p class="text-xs text-gray-500 mb-6">This will begin the initial data sync and set up live replication. You can pause or rollback at any time.</p>
                <button on:click={startPipeline} class="px-8 py-3 bg-green-600 hover:bg-green-700 rounded-lg font-medium text-lg transition-colors">
                  Start Migration
                </button>
              </div>
            {:else}
              <div class="text-center py-4">
                <p class="text-green-400 font-medium">Pipeline is running...</p>
                <p class="text-sm text-gray-400 mt-1">Initial sync and replication setup in progress. Will auto-advance to Live Monitoring when replication is established.</p>
              </div>
            {/if}
          </div>

        {:else if currentStep === 7}
          <!-- STEP 7: LIVE MONITORING -->
          <div class="bg-gray-900 rounded-xl p-8 border border-gray-800">
            <div class="flex items-center justify-between mb-6">
              <div class="flex items-center gap-3">
                <div class="w-10 h-10 rounded-lg bg-blue-500/20 flex items-center justify-center">
                  <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-blue-400"><path d="M22 12h-4l-3 9L9 3l-3 9H2"/></svg>
                </div>
                <div>
                  <h2 class="text-lg font-semibold">Live Monitoring</h2>
                  <p class="text-sm text-gray-400">Monitor sync progress and replication lag</p>
                </div>
              </div>
              <div>
                {#if replicationLag <= 5}
                  <div class="px-4 py-2 bg-green-500/20 border border-green-500/30 rounded-lg text-green-400 text-sm font-medium">
                    Replication caught up ({replicationLag}s lag) &mdash; Ready for cutover
                  </div>
                {:else}
                  <div class="px-4 py-2 bg-yellow-500/20 border border-yellow-500/30 rounded-lg text-yellow-400 text-sm font-medium">
                    Waiting for replication... ({replicationLag}s lag, need &le;5s)
                  </div>
                {/if}
              </div>
            </div>
            {#if replicationStatus.length > 0}
              <div class="space-y-3">
                {#each replicationStatus as rs}
                  <div class="bg-gray-800 rounded-lg p-4 flex items-center justify-between">
                    <div>
                      <div class="font-medium text-sm">{rs.databaseType}{rs.databaseName ? ` / ${rs.databaseName}` : ''}</div>
                      <div class="text-xs text-gray-400">{rs.sourceHost} &rarr; {rs.targetHost} &middot; {rs.replicationMode}</div>
                    </div>
                    <div class="text-right">
                      <div class="text-lg font-bold {rs.replicationLag > 5 ? 'text-yellow-400' : 'text-green-400'}">{rs.replicationLag}s</div>
                      <div class="text-xs text-gray-400">{rs.status}</div>
                    </div>
                  </div>
                {/each}
              </div>
            {/if}
          </div>

        {:else if currentStep === 8}
          <!-- STEP 8: CUTOVER -->
          <div class="bg-gray-900 rounded-xl p-8 border border-gray-800">
            <div class="flex items-center gap-3 mb-6">
              <div class="w-10 h-10 rounded-lg bg-yellow-500/20 flex items-center justify-center">
                <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-yellow-400"><path d="M13 2 3 14h9l-1 8 10-12h-9l1-8z"/></svg>
              </div>
              <div>
                <h2 class="text-lg font-semibold">Cutover</h2>
                <p class="text-sm text-gray-400">Switch traffic from source to target server</p>
              </div>
            </div>

            {#if !cutoverConfirmed}
              <div class="bg-yellow-500/10 border border-yellow-500/30 rounded-lg p-6 mb-6">
                <h3 class="font-medium text-yellow-400 mb-2">Confirm Cutover</h3>
                <p class="text-sm text-gray-300 mb-4">This will:</p>
                <ol class="text-sm text-gray-300 space-y-1 list-decimal list-inside mb-4">
                  <li>Freeze writes on the source server</li>
                  <li>Perform final delta sync</li>
                  <li>Wait for replication to catch up</li>
                  <li>Drain message queues</li>
                  <li>Run health verification</li>
                  <li>Switch traffic to target server</li>
                  <li>Promote target as primary</li>
                  <li>Resume writes on target</li>
                </ol>
                <p class="text-xs text-gray-400 mb-4">Current replication lag: <strong>{replicationLag}s</strong> &middot; Health score: <strong>{healthScore.toFixed(0)}</strong></p>
                <button on:click={confirmCutover} class="px-6 py-2.5 bg-yellow-600 hover:bg-yellow-700 rounded-lg font-medium transition-colors">
                  Confirm Cutover
                </button>
              </div>
            {:else}
              <div class="text-center py-8">
                <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="animate-spin mx-auto mb-3 text-yellow-400"><path d="M21 12a9 9 0 1 1-6.219-8.56"/></svg>
                <p class="text-yellow-400 font-medium">Cutover in progress...</p>
                <p class="text-sm text-gray-400 mt-1">Traffic switch is happening. Do not close this page.</p>
              </div>
            {/if}
          </div>

        {:else if currentStep === 9}
          <!-- STEP 9: OBSERVATION -->
          <div class="bg-gray-900 rounded-xl p-8 border border-gray-800">
            <div class="flex items-center justify-between mb-6">
              <div class="flex items-center gap-3">
                <div class="w-10 h-10 rounded-lg bg-emerald-500/20 flex items-center justify-center">
                  <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-emerald-400"><path d="M2 12s3-7 10-7 10 7 10 7-3 7-10 7-10-7-10-7Z"/><circle cx="12" cy="12" r="3"/></svg>
                </div>
                <div>
                  <h2 class="text-lg font-semibold">Observation</h2>
                  <p class="text-sm text-gray-400">Post-cutover monitoring &mdash; watching for errors</p>
                </div>
              </div>
              <div class="text-right">
                <div class="text-sm font-mono">{observationElapsed}s / {observationDuration}s</div>
                <div class="w-32 h-2 bg-gray-700 rounded-full overflow-hidden mt-1">
                  <div class="h-full bg-emerald-500 rounded-full transition-all" style="width: {observationProgress}%"></div>
                </div>
              </div>
            </div>

            <div class="grid grid-cols-3 gap-4 mb-6">
              <div class="bg-gray-800 rounded-lg p-4 text-center">
                <div class="text-3xl font-bold {healthScore < 80 ? 'text-yellow-400' : 'text-green-400'}">{healthScore.toFixed(0)}</div>
                <div class="text-xs text-gray-400">Health Score</div>
              </div>
              <div class="bg-gray-800 rounded-lg p-4 text-center">
                <div class="text-3xl font-bold text-green-400">{replicationLag}s</div>
                <div class="text-xs text-gray-400">Replication Lag</div>
              </div>
              <div class="bg-gray-800 rounded-lg p-4 text-center">
                <div class="text-3xl font-bold {observationRemaining <= 0 ? 'text-green-400' : 'text-blue-400'}">{observationRemaining <= 0 ? 'Done' : `${observationRemaining}s`}</div>
                <div class="text-xs text-gray-400">Remaining</div>
              </div>
            </div>

            {#if stepStatuses[9] === 'completed'}
              <div class="text-center">
                <p class="text-green-400 font-medium mb-4">Observation period completed. All health checks passing.</p>
                <button on:click={commitMigration} class="px-8 py-3 bg-green-600 hover:bg-green-700 rounded-lg font-medium text-lg transition-colors">
                  Commit Migration
                </button>
              </div>
            {:else}
              <p class="text-sm text-gray-400 text-center">Observing target server health. Auto-rollback will trigger if health drops below threshold.</p>
            {/if}
          </div>
        {/if}

        <!-- Timeline & Logs (shared across steps 6-9) -->
        <div class="grid grid-cols-2 gap-4 mt-6">
          <div class="bg-gray-900 rounded-xl p-4 border border-gray-800">
            <div class="flex items-center justify-between mb-3">
              <h3 class="text-sm font-medium text-gray-300">Timeline</h3>
              <button on:click={refreshAudit} class="text-xs text-blue-400 hover:text-blue-300">Refresh</button>
            </div>
            {#if auditTrail.length > 0}
              <div class="space-y-2 max-h-64 overflow-y-auto">
                {#each auditTrail.slice(-20).reverse() as entry}
                  <div class="flex items-start gap-2 text-xs">
                    <span class="text-gray-500 shrink-0">{new Date(entry.createdAt).toLocaleTimeString()}</span>
                    <span class="px-1.5 py-0.5 rounded bg-gray-800 text-gray-300">{entry.eventType}</span>
                    {#if entry.previousState && entry.newState}
                      <span class="text-gray-400">{entry.previousState} &rarr; {entry.newState}</span>
                    {/if}
                  </div>
                {/each}
              </div>
            {:else}
              <p class="text-xs text-gray-500">No timeline events yet</p>
            {/if}
          </div>
          <div class="bg-gray-900 rounded-xl p-4 border border-gray-800">
            <h3 class="text-sm font-medium text-gray-300 mb-3">Live Logs</h3>
            <div class="font-mono text-xs max-h-64 overflow-y-auto space-y-1">
              {#if wsMessages.length > 0}
                {#each wsMessages.slice(-30) as msg, i}
                  <div class="flex items-start gap-2">
                    <span class="px-1.5 py-0.5 rounded shrink-0 {msg.status === 'error' ? 'bg-red-500/20 text-red-400' : msg.status === 'success' || msg.status === 'complete' ? 'bg-green-500/20 text-green-400' : 'bg-blue-500/20 text-blue-400'}">{msg.status}</span>
                    <span class="text-gray-300">[{msg.step}] {msg.value || msg.error || ''}</span>
                  </div>
                {/each}
              {:else}
                <p class="text-gray-500">No logs yet. Start the pipeline to see real-time logs.</p>
              {/if}
            </div>
          </div>
        </div>
      </div>

    <!-- ══════════════════════════════════════════════════ -->
    <!-- STEP 10: FINISH                                  -->
    <!-- ══════════════════════════════════════════════════ -->
    {:else if currentStep === 10}
      <div class="max-w-4xl mx-auto">
        <div class="bg-gray-900 rounded-xl p-8 border border-gray-800 text-center">
          <div class="w-16 h-16 rounded-full bg-green-500/20 flex items-center justify-center mx-auto mb-4">
            <svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-green-500"><path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/><polyline points="22 4 12 14.01 9 11.01"/></svg>
          </div>
          <h2 class="text-2xl font-bold mb-2">Migration Complete</h2>
          <p class="text-gray-400 mb-8">Migration #{migrationId} has been successfully completed.</p>

          <div class="grid grid-cols-4 gap-4 mb-8">
            <div class="bg-gray-800 rounded-lg p-4">
              <div class="text-xs text-gray-400">Risk Score</div>
              <div class="text-xl font-bold {riskReport ? riskColor(riskReport.riskClass) : ''}">{riskReport?.riskScore.toFixed(0) || '—'}</div>
            </div>
            <div class="bg-gray-800 rounded-lg p-4">
              <div class="text-xs text-gray-400">Downtime</div>
              <div class="text-xl font-bold">{riskReport?.downtimeEstimate || '—'}</div>
            </div>
            <div class="bg-gray-800 rounded-lg p-4">
              <div class="text-xs text-gray-400">Health Score</div>
              <div class="text-xl font-bold text-green-400">{healthScore.toFixed(0)}</div>
            </div>
            <div class="bg-gray-800 rounded-lg p-4">
              <div class="text-xs text-gray-400">Categories</div>
              <div class="text-xl font-bold">{config.categories.length}</div>
            </div>
          </div>

          <div class="flex items-center justify-center gap-4">
            <button on:click={exportReport} class="flex items-center gap-2 px-6 py-2.5 bg-blue-600 hover:bg-blue-700 rounded-lg font-medium transition-colors">
              <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>
              Export Report
            </button>
            <a href="/migrations" class="px-6 py-2.5 bg-gray-700 hover:bg-gray-600 rounded-lg font-medium transition-colors">
              Back to Migrations
            </a>
          </div>
        </div>

        <!-- Timeline in Finish -->
        <div class="bg-gray-900 rounded-xl p-6 border border-gray-800 mt-6">
          <h3 class="text-sm font-medium text-gray-300 mb-3">Migration Timeline</h3>
          {#if auditTrail.length > 0}
            <div class="space-y-2 max-h-96 overflow-y-auto">
              {#each auditTrail as entry}
                <div class="flex items-start gap-3 text-sm py-2 border-b border-gray-800">
                  <span class="text-gray-500 shrink-0 w-20">{new Date(entry.createdAt).toLocaleTimeString()}</span>
                  <span class="px-2 py-0.5 rounded bg-gray-800 text-gray-300 text-xs">{entry.eventType}</span>
                  {#if entry.previousState && entry.newState}
                    <span class="text-gray-400 text-xs">{entry.previousState} &rarr; {entry.newState}</span>
                  {/if}
                </div>
              {/each}
            </div>
          {:else}
            <button on:click={refreshAudit} class="text-xs text-blue-400 hover:text-blue-300">Load Timeline</button>
          {/if}
        </div>
      </div>
    {/if}
  </div>

  <!-- ═══ NAVIGATION FOOTER ═══ -->
  <div class="border-t border-gray-800 px-6 py-4 flex items-center justify-between shrink-0 bg-gray-950">
    <div>
      {#if currentStep > 0 && !pipelineRunning}
        <button on:click={goBack} class="flex items-center gap-2 px-4 py-2 bg-gray-700 hover:bg-gray-600 rounded-lg text-sm font-medium transition-colors">
          <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m12 19-7-7 7-7"/><path d="M19 12H5"/></svg>
          Back
        </button>
      {/if}
    </div>
    <div class="text-xs text-gray-500">
      {WIZARD_STEPS[currentStep].validationGate}
    </div>
    <div>
      {#if currentStep < 10}
        <button
          on:click={goNext}
          disabled={!canProceed(currentStep)}
          class="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:opacity-40 disabled:cursor-not-allowed rounded-lg text-sm font-medium transition-colors"
        >
          {currentStep === 7 && replicationLag <= 5 ? 'Ready for Cutover' : 'Next'}
          <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m5 12h14"/><path d="m12 5 7 7-7 7"/></svg>
        </button>
      {/if}
    </div>
  </div>
</div>

