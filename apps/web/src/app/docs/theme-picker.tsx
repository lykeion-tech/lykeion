'use client';

import { Monitor } from 'lucide-react';
import { useEffect, useRef } from 'react';

const key = 'lykeion-docs-theme';

export function ThemePicker() {
  const control = useRef<HTMLDetailsElement>(null);
  useEffect(() => {
    const media = window.matchMedia('(prefers-color-scheme: dark)');
    const apply = () => {
      const theme = control.current?.querySelector<HTMLInputElement>('input:checked')?.value ?? 'system';
      document.documentElement.dataset.docsTheme = theme === 'system' ? (media.matches ? 'dark' : 'light') : theme;
    };
    try {
      const saved = localStorage.getItem(key);
      if (saved === 'light' || saved === 'dark' || saved === 'system') {
        const input = control.current?.querySelector<HTMLInputElement>(`input[value="${saved}"]`);
        if (input) input.checked = true;
      }
    } catch { /* The preference remains available for this visit. */ }
    apply();
    media.addEventListener('change', apply);
    return () => media.removeEventListener('change', apply);
  }, []);
  return <div className="docs-theme-picker"><details ref={control} className="docs-theme-control" onKeyDown={event => {
    if (event.key === 'Escape' && control.current?.open) { control.current.open = false; control.current.querySelector('summary')?.focus(); }
  }}><summary aria-label="Documentation theme"><Monitor size={20} aria-hidden="true" /></summary><fieldset aria-label="Choose theme">{(['light', 'dark', 'system'] as const).map(next => <label key={next}><input type="radio" name="docs-theme" value={next} defaultChecked={next === 'system'} onClick={() => {
    document.documentElement.dataset.docsTheme = next === 'system' ? (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light') : next;
    try { localStorage.setItem(key, next); } catch { /* Keep the selection for this visit. */ }
    if (control.current) { control.current.open = false; control.current.querySelector('summary')?.focus(); }
  }} /><span>{next[0].toUpperCase() + next.slice(1)}</span></label>)}</fieldset></details></div>;
}
