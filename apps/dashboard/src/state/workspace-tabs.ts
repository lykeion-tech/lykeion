import { ref, watch } from 'vue';
import { readJSON, readText, writeJSON, writeText } from '../storage';

export type Tab = {
  id: number;
  path: string;
  title: string;
  icon: string;
  history: string[];
  index: number;
  pinned: boolean;
};

export const validPath = (value: unknown): value is string =>
  typeof value === 'string' && value.startsWith('/') && !value.startsWith('//');

function restored(): Tab[] {
  const t = readJSON<unknown>('lykeion.tabs', null, 'session');
  try {
    if (
      Array.isArray(t) &&
      t.length &&
      t.every(
        (x) =>
          Number.isFinite(x.id) &&
          validPath(x.path) &&
          typeof x.title === 'string' &&
          typeof x.icon === 'string' &&
          Array.isArray(x.history) &&
          x.history.length > 0 &&
          x.history.every(validPath) &&
          Number.isInteger(x.index) &&
          x.index >= 0 &&
          x.index < x.history.length,
      )
    )
      return t.map((x) => ({
        ...x,
        title:
          x.title === 'Researches' && /^\/projects(?:\/|[?#]|$)/.test(x.path)
            ? 'Projects'
            : x.title,
        pinned: x.pinned === true,
      }));
  } catch {
    // An entry that is not an object fails the shape check; the session starts afresh.
  }
  return [];
}

// Tabs live for the browser tab's session, so they are kept in sessionStorage.
const tabs = ref<Tab[]>(restored());
const savedSelected = Number(readText('lykeion.active-tab', 'session'));
const selected = ref(
  tabs.value.some((t) => t.id === savedSelected) ? savedSelected : (tabs.value[0]?.id ?? 0),
);
let nextId = Math.max(Date.now(), ...tabs.value.map((t) => t.id)) + 1;
watch(selected, (id) => writeText('lykeion.active-tab', String(id), 'session'));
watch(tabs, (x) => writeJSON('lykeion.tabs', x, 'session'), { deep: true });

// Adds a tab and selects it; WorkspaceTabs navigates to whichever tab is selected.
function openTab(path = '/tasks', title = 'Tasks', icon = 'tasks') {
  if (!validPath(path)) return;
  const t: Tab = { id: nextId++, path, title, icon, history: [path], index: 0, pinned: false };
  tabs.value.push(t);
  selected.value = t.id;
}

// Starts the strip with one tab when the session had none.
function ensureTab(path: string, title: string, icon: string) {
  if (!tabs.value.length) openTab(path, title, icon);
}

// The one owner of the session keys 'lykeion.tabs' and 'lykeion.active-tab'.
export function useWorkspaceTabs() {
  return { tabs, selected, openTab, ensureTab };
}
