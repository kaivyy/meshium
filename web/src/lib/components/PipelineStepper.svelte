<script lang="ts">
  import { WIZARD_STEPS } from '$lib/api/pipeline';

  export let currentStep: number;
  export let stepStatuses: ('pending' | 'running' | 'completed' | 'failed')[];
  export let pipelineRunning = false;

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

  export function goToStep(step: number) {
    if (step < currentStep && !pipelineRunning) {
      // This is handled by parent
    }
  }
</script>

<div class="border-b border-gray-800 px-4 py-3 shrink-0 bg-gray-950">
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
        <span class="text-xs font-medium hidden lg:inline {currentStep === i ? 'text-blue-400' : stepStatuses[i] === 'completed' ? 'text-green-400' : 'text-gray-400'}">
          {step.name}
        </span>
      </button>
      {#if i < WIZARD_STEPS.length - 1}
        <div class="flex-1 min-w-4 h-0.5 mx-1 {stepLineColor(stepStatuses[i])} transition-colors"></div>
      {/if}
    {/each}
  </div>
  <div class="mt-1.5 text-center">
    <span class="text-xs font-medium text-blue-400">Step {currentStep + 1}: {WIZARD_STEPS[currentStep].name}</span>
    <span class="text-xs text-gray-500 ml-1.5 hidden md:inline">&mdash; {WIZARD_STEPS[currentStep].description}</span>
  </div>
</div>
