# Meshium Frontend Redesign — Design Spec

**Date:** 2026-07-06
**Status:** Approved for planning
**Scope:** Visual/theming redesign of the SvelteKit frontend (40 routes + shared primitives + app shell). Behavior, data flow, and page structure are preserved. This is a look-and-feel + consistency + accessibility pass, not a feature change.

## Goal

Make all 40 pages feel modern, clean, and professional; consistent across the whole app; WCAG-compliant (Level A, targeting AA contrast); user-friendly; and fully responsive on mobile. Keep the existing page layouts and navigation structure — the user is happy with the structure and only wants a more modern look.

## Problem being solved

The app is visually inconsistent. The shell and 38/40 pages are light theme (`bg-slate-50` shell, `bg-white` cards, `text-slate-900`), while 10 pages (terminal, logs, pipeline, monitoring, docker, drift, assistant, updates, files/[id], migrations/new) use hardcoded dark surfaces (`bg-gray-950`, `bg-gray-900`, `text-white`). Every color is a hardcoded Tailwind utility, so there is no single place to change theming and no way to support a dark mode cleanly.

## Decisions (locked)

- **Theme:** Light + dark toggle. Both must be first-class. Default follows OS `prefers-color-scheme`; user choice persists in `localStorage`.
- **Accent:** Blue (matches existing "M" logo `bg-blue-600`).
- **Style:** Modern/professional — soft depth (subtle borders + shadows), rounded corners consistent with current primitives (`rounded-lg`/`rounded-xl`/`rounded-2xl`), no heavy glassmorphism.
- **Accessibility target:** WCAG Level A compliance, with AA (4.5:1 text / 3:1 UI) color contrast in both themes. Keyboard focus, aria labels, reduced-motion already partly present — extend consistently.
- **Structure:** Unchanged. Same routes, same sidebar groups, same page sections. Restyle only.

## Architecture: semantic design tokens

The core mechanism. Replace hardcoded colors with **semantic CSS custom properties** defined for both themes, exposed to Tailwind as named colors. Components reference intent (`bg-surface`, `text-fg`) not raw palette (`bg-white`, `text-slate-900`). Theme switch = flip a `data-theme`/`.dark` attribute on `<html>`; every token re-resolves, no per-component conditionals.

### Token definitions (`app.css`)

```css
:root {
  --color-bg: 248 250 252;          /* slate-50  */
  --color-surface: 255 255 255;     /* white     */
  --color-surface-muted: 248 250 252;
  --color-fg: 15 23 42;             /* slate-900 */
  --color-fg-muted: 71 85 105;      /* slate-600 */
  --color-fg-subtle: 148 163 184;   /* slate-400 */
  --color-border: 226 232 240;      /* slate-200 */
  --color-border-strong: 203 213 225;
  --color-accent: 37 99 235;        /* blue-600  */
  --color-accent-hover: 29 78 216;  /* blue-700  */
  --color-accent-fg: 255 255 255;
  --color-accent-subtle: 239 246 255; /* blue-50 */
  --color-success: 22 163 74;
  --color-warning: 202 138 4;
  --color-error: 220 38 38;
  --color-info: 37 99 235;
}
.dark {
  --color-bg: 3 7 18;               /* gray-950  */
  --color-surface: 17 24 39;        /* gray-900  */
  --color-surface-muted: 31 41 55;  /* gray-800  */
  --color-fg: 249 250 251;          /* gray-50   */
  --color-fg-muted: 209 213 219;    /* gray-300  */
  --color-fg-subtle: 107 114 128;   /* gray-500  */
  --color-border: 31 41 55;         /* gray-800  */
  --color-border-strong: 55 65 81;  /* gray-700  */
  --color-accent: 59 130 246;       /* blue-500  */
  --color-accent-hover: 96 165 250; /* blue-400  */
  --color-accent-fg: 255 255 255;
  --color-accent-subtle: 30 58 138 / 0.15;
  --color-success: 74 222 128;
  --color-warning: 250 204 21;
  --color-error: 248 113 113;
  --color-info: 96 165 250;
}
```

Values stored as space-separated RGB channels so Tailwind's `<alpha-value>` modifier works (`bg-surface/50`).

### Tailwind wiring (`tailwind.config.ts`)

`darkMode: 'class'`. Extend `theme.colors` with the semantic names, each `rgb(var(--color-x) / <alpha-value>)`. Keep the default Tailwind palette available (some data-viz / status micro-usages may still want raw colors). New names: `bg`, `surface`, `surface-muted`, `fg`, `fg-muted`, `fg-subtle`, `border` (via `borderColor` default), `border-strong`, `accent`, `accent-hover`, `accent-fg`, `accent-subtle`, `success`, `warning`, `error`, `info`.

### Theme controller

- Small module `lib/stores/theme.ts`: `writable<'light'|'dark'|'system'>`, reads `localStorage['meshium-theme']`, applies `.dark` class to `document.documentElement`, listens to `prefers-color-scheme` when in `system` mode.
- Inline no-flash script in `app.html` `<head>` sets the class before first paint (avoids light flash on dark-preference load).
- Toggle control added to `TopBar` (sun/moon/monitor icon) and to the mobile drawer System group.

## Token → color mapping (migration guide)

| Current hardcoded | Light source | Dark source | Semantic token |
|---|---|---|---|
| `bg-white` | white | gray-900 | `bg-surface` |
| `bg-slate-50` (shell/main) | slate-50 | gray-950 | `bg-bg` |
| `bg-slate-50` (table head/inset) | slate-50 | gray-800 | `bg-surface-muted` |
| `bg-gray-950` / `bg-gray-900` (dark pages) | slate-50 / white | gray-950 / gray-900 | `bg-bg` / `bg-surface` |
| `text-slate-900` / `text-white` | slate-900 | gray-50 | `text-fg` |
| `text-slate-600` / `text-gray-300` | slate-600 | gray-300 | `text-fg-muted` |
| `text-slate-400` / `text-gray-500` | slate-400 | gray-500 | `text-fg-subtle` |
| `border-slate-200` / `border-gray-800` | slate-200 | gray-800 | `border-border` |
| `border-slate-300` / `border-gray-700` | slate-300 | gray-700 | `border-border-strong` |
| `bg-blue-600` | blue-600 | blue-500 | `bg-accent` |
| `bg-blue-50` / `text-blue-700` (active nav) | blue-50 / blue-700 | subtle / blue-400 | `bg-accent-subtle` / `text-accent` |
| `text-green/red/yellow-xxx` (status) | 600-ish | 400-ish | `text-success/error/warning` |

Status colors (CPU/RAM meters in LiveMonitor, badges, health chips) map to the `success/warning/error/info` tokens so they stay legible in both themes.

## Components & units of work

Ordered so shared pieces land first — once primitives + shell are on tokens, page sweeps are mechanical.

1. **Foundation** — `app.css` tokens, `tailwind.config.ts`, `app.html` no-flash script, `lib/stores/theme.ts`. Deliverable: theme toggle flips the whole app even before pages are converted (already-tokenized primitives respond).
2. **App shell** — `+layout.svelte` (`main` bg), `Sidebar.svelte`, `TopBar.svelte` (+ theme toggle control), `Toast.svelte`, mobile drawer. Highest visibility; must be flawless in both themes.
3. **UI primitives** (`lib/components/ui/`) — Button, Card, Badge, Modal, DataTable, ProgressBar, Spinner, EmptyState, LogViewer, Skeleton, PageHeader, DropdownMenu. Convert each to tokens. This is the biggest multiplier: most pages compose these.
4. **Pipeline components** (`lib/components/`) — MigrationHeader, PipelineStepper, LiveMonitor, CutoverChecklist, ObservationPanel, DependencyGraphView, BottomTabs. Already dark-only; convert to tokens so they also work in light theme.
5. **Page sweep — light pages (30)** — mechanical token substitution, page by page. Group by nav section to keep review coherent (Overview, Operations, Compare, Tools, System, Insights).
6. **Page sweep — dark pages (10)** — terminal, logs, pipeline, monitoring, docker, drift, assistant, updates, files/[id], migrations/new. These currently assume a dark canvas; convert to tokens so they render correctly in light theme too. Terminal/logs keep an intentionally darker "console" surface via `surface-muted` even in light mode where a code/terminal context justifies it.

## Accessibility work (woven into every unit)

- **Contrast:** all token pairs verified ≥ 4.5:1 (text) / 3:1 (large text, UI borders) in both themes. `fg-subtle` on `surface` is the tightest pair — verify and darken/lighten if needed.
- **Focus-visible:** consistent 2px accent ring on all interactive elements. (Note: current `app.css` sets `:focus-visible { outline: none }` globally with per-element rings — keep the rings, ensure none are missing.)
- **Tap targets:** current CSS enforces `min-height: 32px` on mobile; raise toward 44px (WCAG 2.5.5 AAA is 44, AA is 24 — we target comfortable 44 where layout allows).
- **ARIA:** preserve existing `aria-current`, `aria-label`, `role` usage; add where restyle touches interactive elements lacking them.
- **Reduced motion:** existing `prefers-reduced-motion` block retained; ensure new transitions respect it.
- **Theme toggle:** proper `aria-label`, reflects current state.

## Testing / verification

- `svelte-check` must stay at 0 errors / 0 warnings after every batch.
- `vite build` must succeed and emit all 40 pages.
- Manual visual pass: run the server (port 9527), toggle light/dark, spot-check one page per nav group in both themes at desktop and mobile widths.
- Contrast: verify token pairs with a contrast checker; document ratios in the plan.
- No behavioral regressions: WebSocket flows, forms, tables, modals unchanged — only classes change.

## Out of scope

- No new features, no layout restructuring, no navigation changes.
- No component API changes (props stay identical) unless purely additive (e.g. theme toggle).
- No backend changes.
- No new dependencies (uses existing Tailwind + lucide-svelte).

## Risks

- **Scale:** 40 pages + ~20 components is a large mechanical sweep. Mitigation: tokens land first so per-page work is find-and-replace against the mapping table; batch by nav group; type-check after each batch.
- **Dark pages regressing in light:** the 10 dark pages assume a dark canvas (e.g. white text on dark). Converting them needs care that nothing ends up light-on-light. Mitigation: these get the dedicated batch (unit 6) and explicit both-theme visual check.
- **Contrast on status colors:** green/yellow on light vs dark differ; the token indirection handles this but each status usage must use the token, not a raw `text-green-400`.
