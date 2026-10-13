import { reactive, watch } from 'vue';
import { readJSON } from '../storage';
import { saveSetting } from './settings-saves';

const key = 'lykeion.profile';
const saved = readJSON<unknown>(key, null);
const profile = reactive({
  name: '',
  about: '',
  avatar: '',
  careerStage: '',
  ...(saved && typeof saved === 'object' ? saved : {}),
});
watch(profile, (value) => saveSetting(key, value), { deep: true });

// The one owner of 'lykeion.profile'.
export function useProfile() {
  return profile;
}
