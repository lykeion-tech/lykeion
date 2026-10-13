import { ref } from 'vue';
import { writeJSON } from '../storage';

// Settings reports a failed save; the state it edits counts failures here so it can.
export const failedSettingsSaves = ref(0);

export function saveSetting(key: string, value: unknown) {
  if (!writeJSON(key, value)) failedSettingsSaves.value++;
}
