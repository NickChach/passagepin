export type Theme = 'light' | 'dark';
// Keep preferences saved by releases before the PassagePin rename.
const storageKey = 'pdfsearch.theme';

export function readTheme(): Theme {
  try {
    const saved = window.localStorage.getItem(storageKey);
    if (saved === 'light' || saved === 'dark') return saved;
  } catch { /* A theme can still be switched if preference storage is unavailable. */ }
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

export function applyTheme(theme: Theme): void {
  document.documentElement.dataset.theme = theme;
}

export function saveTheme(theme: Theme): void {
  applyTheme(theme);
  try { window.localStorage.setItem(storageKey, theme); } catch { /* Keep the session preference. */ }
}
