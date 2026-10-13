import { reactive, watch } from 'vue';
import { applyTheme } from '../theme';
import { readJSON, writeText } from '../storage';
import { saveSetting } from './settings-saves';

const key = 'lykeion.preferences';
export type CreateFields = { agent: string[]; manual: string[] };
export const defaultCreateFields: CreateFields = {
  agent: ['Project'],
  manual: ['Status', 'Priority', 'Assignee', 'Labels', 'Project'],
};

function load() {
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const saved = readJSON<unknown>(key, null);
  const stored = saved && typeof saved === 'object' ? (saved as Record<string, unknown>) : {};
  const prefs = {
    theme: 'ash',
    pinComments: true,
    taskOpen: 'page',
    replyMode: 'current',
    timeZone: zone,
    timeZoneAutomatic: true,
    createFields: { ...defaultCreateFields },
    createFieldsByWorkspace: {} as Record<string, CreateFields>,
    ...stored,
  };
  if (
    !prefs.createFields ||
    !Array.isArray(prefs.createFields.agent) ||
    !Array.isArray(prefs.createFields.manual)
  )
    prefs.createFields = { ...defaultCreateFields };
  // Preferences saved before the automatic flag existed count as automatic only in that zone.
  if (typeof stored.timeZoneAutomatic !== 'boolean')
    prefs.timeZoneAutomatic = prefs.timeZone === zone;
  // 'new' was the earlier name of the 'after' reply mode.
  if (prefs.replyMode === 'new') prefs.replyMode = 'after';
  if (!prefs.createFieldsByWorkspace || typeof prefs.createFieldsByWorkspace !== 'object')
    prefs.createFieldsByWorkspace = {};
  return prefs;
}

const preferences = reactive(load());
watch(
  preferences,
  (value) => {
    saveSetting(key, value);
    applyTheme(value.theme);
    writeText('lykeion.theme', document.documentElement.dataset.theme!);
  },
  { deep: true },
);

// The one owner of 'lykeion.preferences' and 'lykeion.theme'.
export function usePreferences() {
  return preferences;
}
