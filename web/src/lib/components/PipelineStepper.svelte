<script lang="ts">
  import { WIZARD_STEPS } from '$lib/api/pipeline';

  export let currentStep: number;
  export let stepStatuses: ('pending' | 'running' | 'completed' | 'failed')[];
  export let pipelineRunning = false;

  function stepIndicatorColor(status: string): string {
    switch (status) {
      case 'completed': return 'bg-success text-accent-fg';
      case 'running': return 'bg-accent text-accent-fg animate-pulse';
      case 'failed': return 'bg-error text-accent-fg';
      default: return 'bg-surface-muted text-fg-subtle';
    }
  }

  function stepLineColor(status: string): string {
    switch (status) {
      case 'completed': return 'bg-success';
      case 'running': return 'bg-accent';
      case 'failed': return 'bg-error';
      default: return 'bg-border';
    }
  }

  export function goToStep(step: number) {
    if (step < currentStep && !pipelineRunning) {
      // This is handled by parent
    }
  }
</script>

<div class="border-b border-border px-4 py-3 shrink-0 bg-bg">
  <div class="flex items-center overflow-x-auto">
    {#each WIZARD_STEPS as step, i}
      <button
        on:click={() => goToStep(i)}
        class="flex items-center gap-2 cursor-pointer shrink-0 {i <= currentStep ? 'opacity-100' : 'opacity-50'}"
        disabled={i > currentStep || pipelineRunning}
      >
        <div class="w-6 h-6 rounded-full flex items-center justify-center text-xs font-bold transition-colors {stepIndicatorColor(stepStatuses[i])}">
          {#if stepStatuses[i] === 'completed'}
            <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"><polyline points="20 6 9 17 4 12"/></svg>
          {:else}
            {i + 1}
          {/if}
        </div>
        <span class="text-xs font-medium hidden lg:inline {currentStep === i ? 'text-accent' : stepStatuses[i] === 'completed' ? 'text-success' : 'text-fg-subtle'}">
          {step.name}
        </span>
      </button>
      {#if i < WIZARD_STEPS.length - 1}
        <div class="flex-1 min-w-4 h-0.5 mx-1 {stepLineColor(stepStatuses[i])} transition-colors"></div>
      {/if}
    {/each}
  </div>
  <div class="mt-1.5 text-center">
    <span class="text-xs font-medium text-accent">Step {currentStep + 1}: {WIZARD_STEPS[currentStep].name}</span>
    <span class="text-xs text-fg-subtle ml-1.5 hidden md:inline">&mdash; {WIZARD_STEPS[currentStep].description}</span>
  </div>
</div>
