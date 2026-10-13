import { readonly, ref } from 'vue';
import { readJSON, writeJSON } from '../storage';

const key = 'lykeion.pinned-tasks';
const saved = readJSON<unknown>(key, []);
const pins = ref<string[]>(
  Array.isArray(saved) ? saved.filter((x): x is string => typeof x === 'string') : [],
);

// Writes before changing the list, so a pin that could not be saved does not appear.
function togglePin(id: string): boolean {
  const next = pins.value.includes(id) ? pins.value.filter((x) => x !== id) : [...pins.value, id];
  if (!writeJSON(key, next)) return false;
  pins.value = next;
  return true;
}

// The one owner of 'lykeion.pinned-tasks': the ids of tasks pinned to the sidebar.
export function usePins() {
  return { pins: readonly(pins), togglePin };
}
