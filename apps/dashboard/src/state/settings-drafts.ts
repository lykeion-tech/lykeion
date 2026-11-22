import { reactive, watch } from 'vue';
import { readJSON } from '../storage';
import { saveSetting } from './settings-saves';

export interface LabelDraft {
  id: string;
  name: string;
  description: string;
  color: string;
  scope: 'Tasks' | 'Skills';
  updatedAt: string;
}
export interface PropertyDraft {
  id: string;
  name: string;
  description?: string;
  icon?: string;
  type: string;
  options: string[];
  optionColors?: string[];
  archived: boolean;
  updatedAt: string;
}
export interface SettingsDraft {
  labels: LabelDraft[];
  properties: PropertyDraft[];
  notifications: Record<string, boolean>;
}

const key = 'lykeion.settings-drafts';
const saved = readJSON<unknown>(key, null);
// Keyed by workspace id; an entry is seeded when Settings first shows that workspace.
const drafts = reactive<Record<string, SettingsDraft>>(
  saved && typeof saved === 'object' ? (saved as Record<string, SettingsDraft>) : {},
);
watch(drafts, (value) => saveSetting(key, value), { deep: true });
