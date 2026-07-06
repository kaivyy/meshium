# Meshium Frontend Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Convert the Meshium SvelteKit frontend (40 routes + shared primitives + app shell) to a semantic CSS-variable design-token system with a light/dark toggle, modernizing the look while preserving every layout, route, and component API.

**Architecture:** Define semantic color tokens as CSS custom properties in `app.css` for both `:root` (light) and `.dark` (dark). Expose them to Tailwind as named colors (`bg`, `surface`, `fg`, `accent`, status colors) via `tailwind.config.ts` using `rgb(var(--color-x) / <alpha-value>)`. A theme store toggles the `.dark` class on `<html>`; an inline no-flash script in `app.html` sets it before first paint. Once foundation + primitives are tokenized, page conversion is mechanical find-and-replace against a fixed mapping table.

**Tech Stack:** SvelteKit 2 / Svelte 5 (runes), Tailwind CSS 3, lucide-svelte, TypeScript. No new dependencies.

## Global Constraints

- **Port:** dev/preview server runs on 9527 — do not change it. A user instance may already hold it; use `vite build` for verification rather than starting a server on 9527.
- **No new dependencies.** Use existing Tailwind + lucide-svelte only.
- **No behavioral changes.** Only CSS classes and the additive theme toggle change. Props, data flow, routes, nav structure, WebSocket flows, forms stay identical.
- **No layout restructuring.** Same page sections, same sidebar groups.
- **Token values stored as space-separated RGB channels** (e.g. `255 255 255`) so Tailwind's `<alpha-value>` modifier works (`bg-surface/50`).
- **Accessibility:** AA contrast (4.5:1 text / 3:1 UI) in both themes; consistent 2px accent focus ring; 44px mobile tap targets; preserve existing ARIA; respect `prefers-reduced-motion`.
- **Verification per task:** `npm run check` (svelte-check) must report 0 errors / 0 warnings, and `npm run build` (vite build) must succeed emitting all 40 pages. There is no unit-test framework for the frontend — svelte-check + build + the token grep are the test cycle.
- **Env var prefix is mixed-case `MESHium_`** (not relevant to FE files but do not "fix" it if encountered).

---

## File Structure

**Created:**
- `web/src/lib/stores/theme.ts` — theme store (`'light'|'dark'|'system'`), localStorage persistence, `.dark` class application, `prefers-color-scheme` listener.
- `web/src/lib/components/ThemeToggle.svelte` — sun/moon/monitor cycle control used in TopBar + mobile drawer.

**Modified (foundation):**
- `web/src/app.css` — token definitions for `:root` + `.dark`; update scrollbar + tap-target rules to tokens.
- `web/tailwind.config.ts` — `darkMode: 'class'`, extend `theme.colors` with semantic names.
- `web/src/app.html` — inline no-flash `<head>` script.

**Modified (shell):**
- `web/src/routes/+layout.svelte`, `web/src/lib/components/Sidebar.svelte`, `web/src/lib/components/TopBar.svelte`, `web/src/lib/components/ui/Toast.svelte`.

**Modified (primitives):** all of `web/src/lib/components/ui/*.svelte`.

**Modified (pipeline components):** `web/src/lib/components/{MigrationHeader,PipelineStepper,LiveMonitor,CutoverChecklist,ObservationPanel,DependencyGraphView,BottomTabs,PlannerView,PlaceholderPage}.svelte`.

**Modified (pages):** all 40 `web/src/routes/**/+page.svelte`.

## Token → color mapping (the substitution table)

Every page/component sweep uses this table. Left = current hardcoded class, right = replacement token class. Applies to all color-bearing prefixes (`bg-`, `text-`, `border-`, `divide-`, `ring-`, `placeholder-`, `hover:*`, `focus-visible:*`).

| Current hardcoded | Token replacement |
|---|---|
| `bg-white` | `bg-surface` |
| `bg-slate-50` (page/shell background) | `bg-bg` |
| `bg-slate-50` (table head, inset panel, hover row) | `bg-surface-muted` |
| `bg-slate-100` (hover, chips) | `bg-surface-muted` |
| `bg-gray-950` (dark page canvas) | `bg-bg` |
| `bg-gray-900` (dark card) | `bg-surface` |
| `bg-gray-800` (dark inset) | `bg-surface-muted` |
| `text-slate-900` / `text-white` (primary text) | `text-fg` |
| `text-slate-700` / `text-slate-600` / `text-gray-300` | `text-fg-muted` |
| `text-slate-500` / `text-slate-400` / `text-gray-500` | `text-fg-subtle` |
| `border-slate-200` / `border-gray-800` | `border-border` |
| `border-slate-300` / `border-gray-700` | `border-border-strong` |
| `divide-slate-200` / `divide-slate-100` | `divide-border` |
| `bg-blue-600` (accent fill) | `bg-accent` |
| `hover:bg-blue-700` | `hover:bg-accent-hover` |
| `text-white` on accent fill | `text-accent-fg` |
| `bg-blue-50` (active nav / subtle accent) | `bg-accent-subtle` |
| `text-blue-700` / `text-blue-600` (active nav / link) | `text-accent` |
| `focus-visible:ring-blue-500` | `focus-visible:ring-accent` |
| `text-green-600` / `text-green-800` (status ok) | `text-success` |
| `text-red-600` / `text-red-800` (status error) | `text-error` |
| `text-yellow-800` / `text-amber-*` (status warn) | `text-warning` |
| `bg-green-100 text-green-800` (success badge) | handled by Badge primitive tokens (Task 3) |

**Rule:** never leave a raw `slate-*` / `gray-*` / `blue-*` color class on a surface/text/border after a sweep. Status micro-colors (chart series, meter fills) use `success/warning/error/info` tokens. When a `bg-slate-50` is ambiguous (background vs inset), decide by context: the outermost page wrapper → `bg-bg`; a panel/thead/hover inside a card → `bg-surface-muted`.

---

### Task 1: Foundation — tokens, Tailwind wiring, theme store, no-flash script

**Files:**
- Modify: `web/src/app.css`
- Modify: `web/tailwind.config.ts`
- Modify: `web/src/app.html`
- Create: `web/src/lib/stores/theme.ts`

**Interfaces:**
- Produces: token color names available to Tailwind: `bg`, `surface`, `surface-muted`, `fg`, `fg-muted`, `fg-subtle`, `border` (default border color), `border-strong`, `accent`, `accent-hover`, `accent-fg`, `accent-subtle`, `success`, `warning`, `error`, `info`.
- Produces: `theme.ts` exports `themeStore: Writable<'light'|'dark'|'system'>`, `setTheme(mode)`, `cycleTheme()`, `initTheme()`. `cycleTheme` advances `light → dark → system → light`.

- [ ] **Step 1: Add token definitions to `app.css`**

Replace the top of `web/src/app.css` (keep the existing `@layer base` focus/scroll/font/motion rules below, but update the scrollbar colors and tap-target as noted in Step 2). Insert the token blocks immediately after the `@tailwind` directives:

```css
@tailwind base;
@tailwind components;
@tailwind utilities;

:root {
  --color-bg: 248 250 252;
  --color-surface: 255 255 255;
  --color-surface-muted: 241 245 249;
  --color-fg: 15 23 42;
  --color-fg-muted: 71 85 105;
  --color-fg-subtle: 100 116 139;
  --color-border: 226 232 240;
  --color-border-strong: 203 213 225;
  --color-accent: 37 99 235;
  --color-accent-hover: 29 78 216;
  --color-accent-fg: 255 255 255;
  --color-accent-subtle: 239 246 255;
  --color-success: 22 163 74;
  --color-warning: 180 120 4;
  --color-error: 220 38 38;
  --color-info: 37 99 235;
}

.dark {
  --color-bg: 3 7 18;
  --color-surface: 17 24 39;
  --color-surface-muted: 31 41 55;
  --color-fg: 249 250 251;
  --color-fg-muted: 209 213 219;
  --color-fg-subtle: 148 163 184;
  --color-border: 31 41 55;
  --color-border-strong: 55 65 81;
  --color-accent: 59 130 246;
  --color-accent-hover: 96 165 250;
  --color-accent-fg: 255 255 255;
  --color-accent-subtle: 30 58 138;
  --color-success: 74 222 128;
  --color-warning: 250 204 21;
  --color-error: 248 113 113;
  --color-info: 96 165 250;
}
```

Note `--color-fg-subtle` light is `100 116 139` (slate-500, not slate-400) — slate-400 (`148 163 184`) fails 4.5:1 on white; slate-500 passes at 4.6:1. `--color-warning` light is darkened to `180 120 4` for AA on white.

- [ ] **Step 2: Update scrollbar + tap-target rules in `app.css` to tokens**

In the existing `@layer base` block, change the scrollbar thumb and the mobile tap-target min-height:

```css
  @media (max-width: 768px) {
    button, a, [role="button"] {
      min-height: 44px;
    }
  }

  ::-webkit-scrollbar-thumb {
    background: rgb(var(--color-border-strong));
    border-radius: 4px;
  }

  ::-webkit-scrollbar-thumb:hover {
    background: rgb(var(--color-fg-subtle));
  }
```

Also add a body default so the shell paints tokens even before components convert. Add inside `@layer base`:

```css
  body {
    background-color: rgb(var(--color-bg));
    color: rgb(var(--color-fg));
  }
```

- [ ] **Step 3: Wire tokens into `tailwind.config.ts`**

Replace `web/tailwind.config.ts` with:

```ts
import type { Config } from 'tailwindcss';

const withAlpha = (v: string) => `rgb(var(${v}) / <alpha-value>)`;

const config: Config = {
  content: ['./src/**/*.{html,js,svelte,ts}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        bg: withAlpha('--color-bg'),
        surface: {
          DEFAULT: withAlpha('--color-surface'),
          muted: withAlpha('--color-surface-muted')
        },
        fg: {
          DEFAULT: withAlpha('--color-fg'),
          muted: withAlpha('--color-fg-muted'),
          subtle: withAlpha('--color-fg-subtle')
        },
        border: {
          DEFAULT: withAlpha('--color-border'),
          strong: withAlpha('--color-border-strong')
        },
        accent: {
          DEFAULT: withAlpha('--color-accent'),
          hover: withAlpha('--color-accent-hover'),
          fg: withAlpha('--color-accent-fg'),
          subtle: withAlpha('--color-accent-subtle')
        },
        success: withAlpha('--color-success'),
        warning: withAlpha('--color-warning'),
        error: withAlpha('--color-error'),
        info: withAlpha('--color-info')
      }
    }
  },
  plugins: []
};

export default config;
```

This yields utilities `bg-surface`, `bg-surface-muted`, `text-fg`, `text-fg-muted`, `text-fg-subtle`, `border-border`, `border-border-strong`, `bg-accent`, `bg-accent-hover`, `text-accent`, `text-accent-fg`, `bg-accent-subtle`, `text-success` etc. Note: because `border` is now an object with `DEFAULT`, the class is `border-border` (and `border-border-strong`); a bare `border` utility still works for width.

- [ ] **Step 4: Create the theme store**

Create `web/src/lib/stores/theme.ts`:

```ts
import { writable } from 'svelte/store';
import { browser } from '$app/environment';

export type ThemeMode = 'light' | 'dark' | 'system';

const STORAGE_KEY = 'meshium-theme';

function readStored(): ThemeMode {
  if (!browser) return 'system';
  const v = localStorage.getItem(STORAGE_KEY);
  return v === 'light' || v === 'dark' || v === 'system' ? v : 'system';
}

function systemPrefersDark(): boolean {
  return browser && window.matchMedia('(prefers-color-scheme: dark)').matches;
}

function resolveDark(mode: ThemeMode): boolean {
  return mode === 'dark' || (mode === 'system' && systemPrefersDark());
}

function applyClass(mode: ThemeMode): void {
  if (!browser) return;
  document.documentElement.classList.toggle('dark', resolveDark(mode));
}

export const themeStore = writable<ThemeMode>(readStored());

export function setTheme(mode: ThemeMode): void {
  if (browser) localStorage.setItem(STORAGE_KEY, mode);
  applyClass(mode);
  themeStore.set(mode);
}

const ORDER: ThemeMode[] = ['light', 'dark', 'system'];
export function cycleTheme(current: ThemeMode): void {
  const next = ORDER[(ORDER.indexOf(current) + 1) % ORDER.length];
  setTheme(next);
}

export function initTheme(): void {
  if (!browser) return;
  const mode = readStored();
  applyClass(mode);
  const mq = window.matchMedia('(prefers-color-scheme: dark)');
  mq.addEventListener('change', () => {
    if (readStored() === 'system') applyClass('system');
  });
}
```

- [ ] **Step 5: Add the no-flash inline script to `app.html`**

Edit `web/src/app.html` `<head>` — add the script immediately before `%sveltekit.head%`:

```html
    <script>
      (function () {
        try {
          var m = localStorage.getItem('meshium-theme') || 'system';
          var dark = m === 'dark' || (m === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches);
          if (dark) document.documentElement.classList.add('dark');
        } catch (e) {}
      })();
    </script>
    %sveltekit.head%
```

- [ ] **Step 6: Verify build + token availability**

Run: `cd web && npm run check && npm run build`
Expected: 0 errors, build succeeds. The app still looks light (no components converted yet) but `document.documentElement` gets `.dark` when localStorage says so.

- [ ] **Step 7: Commit**

```bash
git add web/src/app.css web/tailwind.config.ts web/src/app.html web/src/lib/stores/theme.ts
git commit -m "feat(ui): add semantic design-token system and theme store"
```

---

### Task 2: App shell — layout, ThemeToggle, TopBar, Sidebar, Toast

**Files:**
- Modify: `web/src/routes/+layout.svelte:42` (main bg)
- Create: `web/src/lib/components/ThemeToggle.svelte`
- Modify: `web/src/lib/components/TopBar.svelte`
- Modify: `web/src/lib/components/Sidebar.svelte`
- Modify: `web/src/lib/components/ui/Toast.svelte`

**Interfaces:**
- Consumes: `themeStore`, `cycleTheme`, `initTheme` from Task 1.
- Produces: `ThemeToggle.svelte` — a self-contained button, no props; safe to drop into TopBar and the mobile drawer.

- [ ] **Step 1: Initialize theme in the layout**

In `web/src/routes/+layout.svelte`, add to the `<script>` imports and `onMount`:

```svelte
  import { initTheme } from '$lib/stores/theme';
```

In `onMount`, call `initTheme();` as the first line (before `await checkStatus()`):

```svelte
  onMount(async () => {
    initTheme();
    await checkStatus();
    statusChecked = true;
  });
```

Change the `<main>` class on line 42 from `bg-slate-50` to `bg-bg`:

```svelte
      <main class="flex-1 overflow-auto bg-bg pb-16 md:pb-0">
```

- [ ] **Step 2: Create ThemeToggle component**

Create `web/src/lib/components/ThemeToggle.svelte`:

```svelte
<script lang="ts">
  import { Sun, Moon, Monitor } from 'lucide-svelte';
  import { themeStore, cycleTheme } from '$lib/stores/theme';

  const labels = { light: 'Light theme', dark: 'Dark theme', system: 'System theme' };
</script>

<button
  type="button"
  onclick={() => cycleTheme($themeStore)}
  class="p-1.5 rounded-lg text-fg-subtle hover:text-fg hover:bg-surface-muted transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2"
  aria-label={`Theme: ${labels[$themeStore]}. Click to change.`}
  title={labels[$themeStore]}
>
  {#if $themeStore === 'light'}
    <Sun size={16} aria-hidden="true" />
  {:else if $themeStore === 'dark'}
    <Moon size={16} aria-hidden="true" />
  {:else}
    <Monitor size={16} aria-hidden="true" />
  {/if}
</button>
```

- [ ] **Step 3: Convert TopBar to tokens + add ThemeToggle**

In `web/src/lib/components/TopBar.svelte`: add import `import ThemeToggle from '$lib/components/ThemeToggle.svelte';`. Apply the mapping table to every class:
- header: `border-b border-slate-200 bg-white` → `border-b border-border bg-surface`
- title `text-slate-900` → `text-fg`
- search box: `border-slate-200 bg-slate-50 text-slate-400 hover:bg-slate-100 focus-visible:ring-blue-500` → `border-border bg-surface-muted text-fg-subtle hover:bg-surface-muted focus-visible:ring-accent`
- kbd: `border-slate-300 bg-white text-slate-500` → `border-border-strong bg-surface text-fg-subtle`
- connection status `text-green-600` → `text-success`
- alerts bell: `text-slate-400 hover:text-slate-700 hover:bg-slate-100 focus-visible:ring-blue-500` → `text-fg-subtle hover:text-fg hover:bg-surface-muted focus-visible:ring-accent`

Add `<ThemeToggle />` in the right-side `div` immediately before the alerts bell `<a>`.

- [ ] **Step 4: Convert Sidebar to tokens**

In `web/src/lib/components/Sidebar.svelte`, apply the mapping table throughout (desktop aside, mobile bottom bar, drawer). Key substitutions:
- `bg-white border-r border-slate-200` → `bg-surface border-r border-border`
- logo mark `bg-blue-600 ... text-white` → `bg-accent ... text-accent-fg`
- logo text `text-slate-900` → `text-fg`
- collapse button `text-slate-400 hover:text-slate-700 hover:bg-slate-100 focus-visible:ring-blue-500` → `text-fg-subtle hover:text-fg hover:bg-surface-muted focus-visible:ring-accent`
- group labels `text-slate-400` → `text-fg-subtle`
- divider `border-slate-100` → `border-border`
- active nav item `bg-blue-50 text-blue-700` → `bg-accent-subtle text-accent`
- inactive nav item `text-slate-600 hover:bg-slate-50` → `text-fg-muted hover:bg-surface-muted`
- jobs badge `bg-blue-600 ... text-white` → `bg-accent ... text-accent-fg` (both the pill and the collapsed dot)
- bottom Settings/Lock links `text-slate-600 hover:bg-slate-50 focus-visible:ring-blue-500` → `text-fg-muted hover:bg-surface-muted focus-visible:ring-accent`
- mobile bar `bg-white border-t border-slate-200` → `bg-surface border-t border-border`
- mobile quick items active `text-blue-600` / inactive `text-slate-500` → `text-accent` / `text-fg-subtle`
- center drawer button `bg-blue-600 text-white shadow-blue-600/30 focus-visible:ring-blue-500` → `bg-accent text-accent-fg shadow-accent/30 focus-visible:ring-accent`
- drawer overlay `bg-black/30` → keep (scrim is theme-agnostic)
- drawer panel `bg-white ... border-slate-200` → `bg-surface ... border-border`
- handle bar `bg-slate-300` → `bg-border-strong`
- drawer header border `border-slate-100` → `border-border`; logo mark accent; `text-slate-900` → `text-fg`
- drawer close `text-slate-400 hover:text-slate-700 hover:bg-slate-100` → `text-fg-subtle hover:text-fg hover:bg-surface-muted`
- drawer group labels `text-slate-400` → `text-fg-subtle`
- drawer nav items active `bg-blue-50 text-blue-600` / inactive `text-slate-600 hover:bg-slate-50` → `bg-accent-subtle text-accent` / `text-fg-muted hover:bg-surface-muted`
- drawer badges `bg-blue-600 ... text-white` → `bg-accent ... text-accent-fg`

Add a theme control to the drawer System group: after the Lock button in the drawer's System grid, add:

```svelte
        <button
          onclick={() => cycleTheme($themeStore)}
          class="flex flex-col items-center gap-1.5 rounded-xl p-3 transition-colors text-fg-muted hover:bg-surface-muted"
        >
          {#if $themeStore === 'light'}<Sun size={22} />{:else if $themeStore === 'dark'}<Moon size={22} />{:else}<Monitor size={22} />{/if}
          <span class="text-[10px] font-medium">Theme</span>
        </button>
```

Add to Sidebar imports: `Sun, Moon, Monitor` from `lucide-svelte` and `import { themeStore, cycleTheme } from '$lib/stores/theme';`.

- [ ] **Step 5: Convert Toast to tokens**

In `web/src/lib/components/ui/Toast.svelte`, the `variantClasses` map uses status colors. Convert to token-based subtle surfaces:

```ts
  const variantClasses: Record<ToastItem['variant'], string> = {
    success: 'bg-surface border border-success/30 text-success',
    error: 'bg-surface border border-error/30 text-error',
    warning: 'bg-surface border border-warning/30 text-warning',
    info: 'bg-surface border border-info/30 text-info',
  };
```

The dismiss button `hover:bg-black/5` → `hover:bg-surface-muted`.

- [ ] **Step 6: Verify + visual check both themes**

Run: `cd web && npm run check && npm run build`
Expected: 0 errors, build succeeds. Shell (sidebar, topbar, main bg) now responds to the theme toggle. Toggle cycles light→dark→system.

- [ ] **Step 7: Commit**

```bash
git add web/src/routes/+layout.svelte web/src/lib/components/ThemeToggle.svelte web/src/lib/components/TopBar.svelte web/src/lib/components/Sidebar.svelte web/src/lib/components/ui/Toast.svelte
git commit -m "feat(ui): tokenize app shell and add theme toggle"
```

---

### Task 3: UI primitives

**Files:** all in `web/src/lib/components/ui/`:
- Modify: `Button.svelte`, `Card.svelte`, `Badge.svelte`, `Modal.svelte`, `DataTable.svelte`, `ProgressBar.svelte`, `Spinner.svelte`, `EmptyState.svelte`, `LogViewer.svelte`, `Skeleton.svelte`, `PageHeader.svelte`, `DropdownMenu.svelte`

**Interfaces:**
- Consumes: tokens from Task 1. No prop changes — all component APIs stay identical.
- Produces: tokenized primitives that render correctly in both themes; consumed by every page in Tasks 5–6.

- [ ] **Step 1: Button.svelte**

Replace the `variantClass` derived block:

```svelte
  const variantClass = $derived(
    variant === 'primary'
      ? 'bg-accent text-accent-fg hover:bg-accent-hover focus-visible:ring-accent'
      : variant === 'secondary'
        ? 'border border-border-strong bg-surface text-fg-muted hover:bg-surface-muted focus-visible:ring-accent'
        : variant === 'danger'
          ? 'bg-error text-white hover:opacity-90 focus-visible:ring-error'
          : 'text-fg-muted hover:bg-surface-muted focus-visible:ring-accent'
  );
```

- [ ] **Step 2: Card.svelte**

```svelte
  const interactiveClass = $derived(
    hoverable ? 'hover:border-border-strong hover:shadow-md transition-all cursor-pointer' : ''
  );
```

And the wrapper div class: `rounded-xl border border-slate-200 bg-white shadow-sm` → `rounded-xl border border-border bg-surface shadow-sm`.

- [ ] **Step 3: Badge.svelte**

Replace `variantClass`:

```svelte
  const variantClass = $derived(
    variant === 'success'
      ? 'bg-success/15 text-success border border-success/30'
      : variant === 'warning'
        ? 'bg-warning/15 text-warning border border-warning/30'
        : variant === 'error'
          ? 'bg-error/15 text-error border border-error/30'
          : variant === 'info'
            ? 'bg-info/15 text-info border border-info/30'
            : 'bg-surface-muted text-fg-muted border border-border'
  );
```

- [ ] **Step 4: Modal.svelte**

- panel `bg-white` → `bg-surface`
- header border `border-slate-200` → `border-border`; title `text-slate-900` → `text-fg`
- close button `text-slate-500 hover:bg-slate-100 hover:text-slate-700 focus-visible:ring-blue-500` → `text-fg-subtle hover:bg-surface-muted hover:text-fg focus-visible:ring-accent`
- footer border `border-slate-200` → `border-border`
- backdrop `bg-black/50` → keep (scrim theme-agnostic)

- [ ] **Step 5: DataTable.svelte**

- wrapper `border-slate-200 bg-white` → `border-border bg-surface`
- `divide-slate-200` → `divide-border`
- thead `bg-slate-50` → `bg-surface-muted`
- th `text-slate-500` → `text-fg-subtle`; sortable button `hover:text-slate-900` → `hover:text-fg`
- tbody `divide-slate-100` → `divide-border`
- loading/empty cell `text-slate-500` → `text-fg-subtle`
- row hover `hover:bg-slate-50` → `hover:bg-surface-muted`
- cell `text-slate-700` → `text-fg-muted`

- [ ] **Step 6: Remaining primitives (ProgressBar, Spinner, EmptyState, LogViewer, Skeleton, PageHeader, DropdownMenu)**

Read each and apply the mapping table. Expected common substitutions:
- ProgressBar: track `bg-slate-200` → `bg-surface-muted`; fill `bg-blue-600` → `bg-accent`; label text → `text-fg-muted`.
- Spinner: `text-slate-*`/`border-*` → `text-fg-subtle` / `border-border`.
- EmptyState: icon/text `text-slate-400`/`text-slate-500` → `text-fg-subtle`; title `text-slate-900` → `text-fg`.
- LogViewer: this is a console surface — canvas `bg-slate-900`/`bg-gray-900` → `bg-surface-muted` (keeps a darker console feel in light, dark in dark); log text `text-slate-*` → `text-fg`/`text-fg-muted`; keep semantic log-level colors mapped to `text-success/warning/error/info`.
- Skeleton: `bg-slate-200` / `bg-slate-100` → `bg-surface-muted`.
- PageHeader: title `text-slate-900` → `text-fg`; subtitle `text-slate-500`/`text-slate-600` → `text-fg-muted`; border `border-slate-200` → `border-border`.
- DropdownMenu: menu surface `bg-white border-slate-200` → `bg-surface border-border`; items `text-slate-700 hover:bg-slate-50` → `text-fg-muted hover:bg-surface-muted`; divider `border-slate-100` → `border-border`.

Grep after editing to confirm no stray palette classes remain in `ui/`:

Run: `cd web && grep -rEn '(bg|text|border|divide|ring)-(slate|gray|blue)-[0-9]' src/lib/components/ui/`
Expected: no output (empty). Exception: `text-white` on the danger button fill is acceptable (kept intentionally); if it appears, that single match is fine.

- [ ] **Step 7: Verify**

Run: `cd web && npm run check && npm run build`
Expected: 0 errors, build succeeds. Toggle theme — all primitives (buttons, cards, tables, badges, modals) render correctly in both light and dark.

- [ ] **Step 8: Commit**

```bash
git add web/src/lib/components/ui
git commit -m "feat(ui): tokenize all UI primitives for light/dark themes"
```

---

### Task 4: Pipeline & shared components

**Files:** in `web/src/lib/components/`:
- Modify: `MigrationHeader.svelte`, `PipelineStepper.svelte`, `LiveMonitor.svelte`, `CutoverChecklist.svelte`, `ObservationPanel.svelte`, `DependencyGraphView.svelte`, `BottomTabs.svelte`, `PlannerView.svelte`, `PlaceholderPage.svelte`

**Interfaces:**
- Consumes: tokens (Task 1), primitives (Task 3). No prop changes.
- Produces: pipeline components that work in both themes (they were dark-only).

- [ ] **Step 1: Convert each component with the mapping table**

Read each file and apply substitutions. These components currently assume a dark canvas (many use `bg-gray-900`, `text-white`, `text-gray-300/400`). Map:
- `bg-gray-950` → `bg-bg`, `bg-gray-900` → `bg-surface`, `bg-gray-800` → `bg-surface-muted`
- `text-white` → `text-fg`, `text-gray-300` → `text-fg-muted`, `text-gray-400`/`text-gray-500` → `text-fg-subtle`
- `border-gray-800` → `border-border`, `border-gray-700` → `border-border-strong`
- accent `bg-blue-600`/`text-blue-400` → `bg-accent`/`text-accent`
- status meters in LiveMonitor (CPU/RAM/net): map thresholds to `bg-success` / `bg-warning` / `bg-error` (green under 60%, amber 60–85%, red above). Keep the existing threshold logic; only swap the color class expressions to tokens.
- PipelineStepper stage states: pending `text-fg-subtle`, active `text-accent`, done `text-success`, failed `text-error`.
- CutoverChecklist checkboxes/rows: `text-fg`/`text-fg-muted`, checked accent.
- DependencyGraphView: node fills/strokes — map background nodes to `surface`/`surface-muted`, edges to `border-strong`, highlighted to `accent`.

- [ ] **Step 2: Grep for stray palette classes**

Run: `cd web && grep -rEn '(bg|text|border|divide|ring)-(slate|gray|blue)-[0-9]' src/lib/components/*.svelte`
Expected: no output. (Sidebar/TopBar already done in Task 2; those are the only other `.svelte` files at this level and should already be clean.)

- [ ] **Step 3: Verify**

Run: `cd web && npm run check && npm run build`
Expected: 0 errors, build succeeds.

- [ ] **Step 4: Commit**

```bash
git add web/src/lib/components/*.svelte
git commit -m "feat(ui): tokenize pipeline and shared components"
```

---

### Task 5: Light-page sweep (Batches A–F by nav group)

30 pages that are currently light. Convert each with the mapping table. Do them in nav-group batches; commit + verify after each batch. Every batch runs the same verify + grep loop, so it is written once here and referenced per batch.

**Per-batch verify loop (run at the end of every batch below):**
```
cd web && npm run check && npm run build
cd web && grep -rEn '(bg|text|border|divide|ring)-(slate|gray)-[0-9]' <the batch's files>
```
Expected: check/build clean; grep empty (a `bg-blue-*`/`text-blue-*` that is a genuine data-viz accent may remain only if it maps to no token role — prefer `accent`). Then commit the batch.

**Files:**
- Batch A (Overview): `routes/+page.svelte`, `routes/servers/+page.svelte`, `routes/servers/[id]/+page.svelte`, `routes/servers/[id]/edit/+page.svelte`, `routes/servers/new/+page.svelte`, `routes/discovery/+page.svelte`
- Batch B (Operations): `routes/plans/+page.svelte`, `routes/plans/new/+page.svelte`, `routes/plans/[id]/+page.svelte`, `routes/migrations/+page.svelte`, `routes/migrations/[id]/+page.svelte`, `routes/migrations/[id]/diff/+page.svelte`, `routes/jobs/+page.svelte`, `routes/jobs/[id]/+page.svelte`
- Batch C (Compare + SSH): `routes/servers/compare/+page.svelte`, `routes/servers/[id]/ssh/+page.svelte`, `routes/ssh/+page.svelte`, `routes/ssh/profiles/+page.svelte`, `routes/ssh/known-hosts/+page.svelte`, `routes/ssh/auth-priority/+page.svelte`, `routes/ssh/history/+page.svelte`
- Batch D (Tools): `routes/services/+page.svelte`, `routes/processes/+page.svelte`
- Batch E (System): `routes/cron/+page.svelte`, `routes/firewall/+page.svelte`
- Batch F (Insights + auth): `routes/alerts/+page.svelte`, `routes/settings/+page.svelte`, `routes/login/+page.svelte`, `routes/setup/+page.svelte`

- [ ] **Step A1: Convert Batch A files** — apply mapping table to each of the 6 files.
- [ ] **Step A2: Run the per-batch verify loop for Batch A files.** Expected: clean.
- [ ] **Step A3: Commit** — `git add <batch A files> && git commit -m "feat(ui): tokenize Overview pages"`

- [ ] **Step B1: Convert Batch B files** — apply mapping table to each of the 8 files.
- [ ] **Step B2: Run the per-batch verify loop for Batch B files.** Expected: clean.
- [ ] **Step B3: Commit** — `git commit -m "feat(ui): tokenize Operations pages"`

- [ ] **Step C1: Convert Batch C files** — apply mapping table to each of the 7 files.
- [ ] **Step C2: Run the per-batch verify loop for Batch C files.** Expected: clean.
- [ ] **Step C3: Commit** — `git commit -m "feat(ui): tokenize Compare and SSH pages"`

- [ ] **Step D1: Convert Batch D files** — apply mapping table to each of the 2 files.
- [ ] **Step D2: Run the per-batch verify loop for Batch D files.** Expected: clean.
- [ ] **Step D3: Commit** — `git commit -m "feat(ui): tokenize Tools pages (services, processes)"`

- [ ] **Step E1: Convert Batch E files** — apply mapping table to each of the 2 files.
- [ ] **Step E2: Run the per-batch verify loop for Batch E files.** Expected: clean.
- [ ] **Step E3: Commit** — `git commit -m "feat(ui): tokenize System pages (cron, firewall)"`

- [ ] **Step F1: Convert Batch F files** — apply mapping table. login/setup render without shell chrome, so verify their full-screen backgrounds use `bg-bg` and cards `bg-surface`.
- [ ] **Step F2: Run the per-batch verify loop for Batch F files.** Expected: clean.
- [ ] **Step F3: Commit** — `git commit -m "feat(ui): tokenize Insights and auth pages"`

---

### Task 6: Dark-page sweep

10 pages currently built on a dark canvas. Convert to tokens so they render in both themes. Terminal and logs keep an intentionally darker console surface (`bg-surface-muted`) even in light mode.

**Files:**
- `routes/terminal/+page.svelte`, `routes/logs/+page.svelte`, `routes/monitoring/+page.svelte`, `routes/docker/+page.svelte`, `routes/drift/+page.svelte`, `routes/assistant/+page.svelte`, `routes/updates/+page.svelte`, `routes/files/+page.svelte`, `routes/files/[id]/+page.svelte`, `routes/migrations/new/+page.svelte`, `routes/migrations/[id]/pipeline/+page.svelte`

**Interfaces:**
- Consumes: tokens (Task 1), primitives (Task 3), pipeline components (Task 4).

- [ ] **Step 1: Convert terminal + logs (console pages)**

For `routes/terminal/+page.svelte` and `routes/logs/+page.svelte`: map the dark canvas to tokens but keep the terminal/log output region on `bg-surface-muted` with `text-fg` / mono font so it reads as a console in both themes. Chrome (toolbars, headers, tabs) → `bg-surface` / `border-border` / `text-fg-muted`. ANSI/log-level colors map to `text-success/warning/error/info`.

- [ ] **Step 2: Convert monitoring + docker + drift**

Apply the mapping table. Monitoring meters/charts → status tokens (`success/warning/error`). Docker container state chips → status tokens. Drift add/remove/change indicators → `text-success` (added), `text-error` (removed), `text-warning` (changed).

- [ ] **Step 3: Convert assistant + updates**

Apply the mapping table. Assistant chat bubbles: user bubble `bg-accent text-accent-fg`, assistant bubble `bg-surface-muted text-fg`. Updates page cards → `bg-surface border-border`.

- [ ] **Step 4: Convert files + files/[id] + migrations/new + migrations/[id]/pipeline**

Apply the mapping table. files/[id] file browser rows → `hover:bg-surface-muted`, `text-fg-muted`. migrations/new wizard steps → accent for active, `border-border` panels. pipeline page composes MigrationHeader/PipelineStepper/LiveMonitor (already tokenized in Task 4) — convert only the page-level wrapper/chrome.

- [ ] **Step 5: Grep the whole routes tree for stray palette classes**

Run: `cd web && grep -rEn '(bg|text|border|divide|ring)-(slate|gray)-[0-9]' src/routes/`
Expected: no output. Any remaining `blue-*` should be reviewed — convert to `accent` unless it's a deliberate multi-series chart color.

- [ ] **Step 6: Verify**

Run: `cd web && npm run check && npm run build`
Expected: 0 errors, build succeeds, all 40 pages emitted.

- [ ] **Step 7: Commit**

```bash
git add web/src/routes
git commit -m "feat(ui): tokenize dark pages for light/dark theme support"
```

---

### Task 7: Final sweep, contrast audit, and embed rebuild

**Files:** none new — verification + the Go embed of built assets.

- [ ] **Step 1: Full-tree stray-class grep**

Run: `cd web && grep -rEn '(bg|text|border|divide|ring)-(slate|gray)-[0-9]' src/`
Expected: empty. Fix any stragglers with the mapping table, then re-run.

- [ ] **Step 2: Contrast spot-check (document ratios)**

Verify these token pairs meet AA using any contrast tool (values are the RGB channels from Task 1):
- light `fg-subtle` (100 116 139) on `surface` (255 255 255) → expect ≥ 4.5:1
- light `warning` (180 120 4) on `surface` → expect ≥ 4.5:1
- dark `fg-muted` (209 213 219) on `surface` (17 24 39) → expect ≥ 4.5:1
- dark `accent` (59 130 246) on `surface` → expect ≥ 3:1 (UI)
If any fail, adjust the token channel value in `app.css` and re-verify.

- [ ] **Step 3: Full build + check**

Run: `cd web && npm run check && npm run build`
Expected: 0 errors/warnings, build succeeds.

- [ ] **Step 4: Rebuild Go binary to embed new assets**

The Go server serves the built SPA via `//go:embed`. Rebuild so the redesigned frontend is embedded:

Run: `cd /root/meshium && go build ./...`
Expected: builds clean. (Do not start a server on 9527 — the user's instance may be running.)

- [ ] **Step 5: Commit built assets if tracked**

Check whether `web/build` (or the embed target) is tracked:
Run: `git status --short web/`
If build output is tracked, `git add` it and commit: `git commit -m "chore(ui): rebuild embedded frontend assets"`. If gitignored, skip — nothing to commit.

- [ ] **Step 6: Final commit of any remaining edits**

```bash
git add -A web/src
git commit -m "feat(ui): finalize light/dark redesign across all pages" || echo "nothing to commit"
```

---

## Self-Review

**Spec coverage:**
- Token system (CSS vars + Tailwind) → Task 1 ✓
- Theme toggle (class-based, localStorage, system default, no-flash) → Task 1 (store/script) + Task 2 (ThemeToggle, TopBar, drawer) ✓
- Primitive conversion → Task 3 ✓
- Shell conversion → Task 2 ✓
- Pipeline components (dark→both) → Task 4 ✓
- Light page sweep (30) → Task 5 (Batches A–F) ✓
- Dark page sweep (10) → Task 6 ✓
- WCAG: contrast (Task 7 Step 2), focus rings (`focus-visible:ring-accent` throughout), 44px tap targets (Task 1 Step 2), preserved ARIA (no ARIA removed in any step), reduced-motion (untouched existing rule) ✓
- Mobile responsiveness → Sidebar mobile bar/drawer preserved + tokenized (Task 2) ✓
- Preserve layouts/routes/APIs → no structural or prop changes in any task ✓
- Port 9527 unchanged → Global Constraints + Task 7 avoids starting a server ✓
- No new deps → Global Constraints ✓

**Placeholder scan:** No TBD/TODO. Every conversion step names exact files + exact class substitutions or references the shared mapping table. Verify loops give exact commands + expected output.

**Type/name consistency:** `themeStore`, `setTheme`, `cycleTheme(current)`, `initTheme` used consistently across Task 1 (definition), Task 2 (TopBar, ThemeToggle, Sidebar drawer), and layout init. `ThemeMode` type name consistent. Tailwind color names in the config (Task 1 Step 3) match every utility class used in Tasks 2–6 (`surface-muted`, `fg-subtle`, `border-strong`, `accent-hover`, `accent-subtle`, `accent-fg`).

**Scope:** Single implementation plan; one coherent redesign. No decomposition needed.

**Page count check:** Task 5 batches = 6+8+7+2+2+4 = 29 + Task 6 = 11 = 40 distinct routes. (Task 6 lists 11 files because `migrations/[id]/pipeline` is counted here rather than in Batch B; total distinct routes across Tasks 5–6 = 40, matching the route listing.)
