// encatch: Tailwind build for the My Tickets pages. The theme mirrors libredesk's
// frontend/tailwind.config.cjs so the pages match the agent app (shadcn-vue, new-york).
// Dark mode follows the visitor's system setting (no JavaScript on these pages).
// Rebuild after changing templates: ./build.sh
const hsl = (v) => `hsl(var(--${v}))`

module.exports = {
  darkMode: 'media',
  content: ['../templates/*.html', './app.css'],
  theme: {
    extend: {
      fontFamily: { sans: ['Geist', 'ui-sans-serif', 'system-ui', 'sans-serif', '"Apple Color Emoji"', '"Segoe UI Emoji"'] },
      colors: {
        border: hsl('border'), input: hsl('input'), ring: hsl('ring'),
        background: hsl('background'), foreground: hsl('foreground'),
        primary: { DEFAULT: hsl('primary'), foreground: hsl('primary-foreground') },
        secondary: { DEFAULT: hsl('secondary'), foreground: hsl('secondary-foreground') },
        destructive: { DEFAULT: hsl('destructive'), foreground: hsl('destructive-foreground') },
        success: { DEFAULT: hsl('success'), foreground: hsl('success-foreground') },
        warning: { DEFAULT: hsl('warning'), foreground: hsl('warning-foreground') },
        muted: { DEFAULT: hsl('muted'), foreground: hsl('muted-foreground') },
        accent: { DEFAULT: hsl('accent'), foreground: hsl('accent-foreground') },
        card: { DEFAULT: hsl('card'), foreground: hsl('card-foreground') },
        link: hsl('link'),
      },
      borderRadius: { lg: 'var(--radius)', md: 'calc(var(--radius) - 2px)', sm: 'calc(var(--radius) - 4px)' },
    },
  },
}
