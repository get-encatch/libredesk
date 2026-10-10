// encatch: Tailwind build for the My Tickets pages: shadcn component styles with
// Encatch's design tokens (see app.css), not libredesk's agent-app theme.
// Dark mode is CSS tokens in app.css: the visitor's choice (data-theme on <html>, from a
// cookie) or else the device setting. No dark: utilities are used.
// Rebuild after changing templates: ./build.sh
const v = (name) => `var(--${name})`

module.exports = {
  darkMode: 'media',
  content: ['../templates/*.html', './app.css'],
  theme: {
    extend: {
      fontFamily: { sans: ['Geist', 'ui-sans-serif', 'system-ui', 'sans-serif', '"Apple Color Emoji"', '"Segoe UI Emoji"'] },
      colors: {
        border: v('border'), input: v('input'), ring: v('ring'),
        background: v('background'), foreground: v('foreground'),
        primary: { DEFAULT: v('primary'), foreground: v('primary-foreground') },
        secondary: { DEFAULT: v('secondary'), foreground: v('secondary-foreground') },
        destructive: { DEFAULT: v('destructive'), foreground: v('destructive-foreground') },
        success: { DEFAULT: v('success'), foreground: v('success-foreground') },
        warning: { DEFAULT: v('warning'), foreground: v('warning-foreground') },
        info: { DEFAULT: v('info'), foreground: v('info-foreground') },
        muted: { DEFAULT: v('muted'), foreground: v('muted-foreground') },
        accent: { DEFAULT: v('accent'), foreground: v('accent-foreground') },
        card: { DEFAULT: v('card'), foreground: v('card-foreground') },
        sidebar: v('sidebar'),
        'badge-primary': { bg: v('badge-primary-bg'), text: v('badge-primary-text'), border: v('badge-primary-border') },
        'badge-accent': { bg: v('badge-accent-bg'), text: v('badge-accent-text'), border: v('badge-accent-border') },
        'badge-neutral': { bg: v('badge-neutral-bg'), text: v('badge-neutral-text'), border: v('badge-neutral-border') },
      },
      letterSpacing: { tight: 'calc(var(--tracking-normal) - 0.025em)', normal: 'var(--tracking-normal)' },
      borderRadius: { xl: 'calc(var(--radius) + 4px)', lg: 'var(--radius)', md: 'calc(var(--radius) - 2px)', sm: 'calc(var(--radius) - 4px)' },
    },
  },
}
