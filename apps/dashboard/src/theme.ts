export interface Theme {
  id: 'ash' | 'midnight' | 'dawn' | 'pale';
  name: string;
  description: string;
  surface: string;
  canvas: string;
  input: string;
  text: string;
  muted: string;
  border: string;
  accent: string;
  // The accent as text and focus ring, a step of its hue that reads at 4.5:1 on surface, canvas and input.
  accentText: string;
  selected: string;
}

// The single source of the palettes: applyTheme writes them as CSS custom properties.
export const themes: Theme[] = [
  {
    id: 'ash',
    name: 'Ash',
    description: "Clean white light theme in the logo's blue.",
    surface: '#ffffff',
    canvas: '#f2f2f2',
    input: '#edeef2',
    text: '#44494d',
    muted: '#666a6d',
    border: '#e9e9eb',
    accent: '#3274a4',
    accentText: '#30709f',
    selected: '#c9cacf',
  },
  {
    id: 'midnight',
    name: 'Midnight',
    description: 'Near-black with a rose accent.',
    surface: '#0f0f0f',
    canvas: '#1e1e21',
    input: '#151518',
    text: '#f0f0f2',
    muted: '#bdbdbf',
    border: '#2a292c',
    accent: '#d25f66',
    accentText: '#d36369',
    selected: '#444447',
  },
  {
    id: 'dawn',
    name: 'Dawn',
    description: 'Warm plum dark theme with a magenta accent.',
    surface: '#2a232c',
    canvas: '#38323b',
    input: '#382a3c',
    text: '#f0eef3',
    muted: '#c6bec9',
    border: '#413b44',
    accent: '#aa4278',
    accentText: '#d086ac',
    selected: '#615662',
  },
  {
    id: 'pale',
    name: 'Pale',
    description: 'Cool indigo dark theme with a violet accent.',
    surface: '#2a2d40',
    canvas: '#373b4a',
    input: '#2a2d40',
    text: '#eceef7',
    muted: '#c6c8d4',
    border: '#414456',
    accent: '#8054c4',
    accentText: '#b399dc',
    selected: '#545866',
  },
];

export function themeById(id: unknown): Theme {
  return themes.find((theme) => theme.id === id) || themes[0]!;
}

export function applyTheme(id: unknown) {
  const theme = themeById(id);
  const root = document.documentElement;
  root.dataset.theme = theme.id === 'ash' ? 'light' : 'dark';
  root.dataset.palette = theme.id;
  for (const key of ['surface', 'canvas', 'input', 'text', 'muted', 'border', 'selected'] as const)
    root.style.setProperty('--' + key, theme[key]);
  root.style.setProperty('--primary', theme.accent);
  root.style.setProperty('--primary-text', theme.accentText);
  root.style.setProperty('--hover', theme.input);
}
