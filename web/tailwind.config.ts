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
