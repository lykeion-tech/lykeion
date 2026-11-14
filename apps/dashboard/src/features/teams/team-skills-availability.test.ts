import { expect, it, vi } from 'vitest';
import { ref } from 'vue';
import { createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import TeamSkillsPicker from './TeamSkillsPicker.vue';

vi.mock('../../api/queries', () => ({
  useSkillLibrary: () => ({
    data: ref([{ name: 'Research', policy_enabled: false }]),
    isPending: ref(false),
    isError: ref(false),
    isSuccess: ref(true),
  }),
}));

it('offers skills even if an old workspace policy marked them unavailable', () => {
  vi.stubGlobal('document', {
    activeElement: null,
    addEventListener() {},
    removeEventListener() {},
  });
  const root = node('root');
  const app = createTreeApp(TeamSkillsPicker, {
    member: { name: 'agent', skills: [] },
    busy: false,
    error: '',
  });
  app.mount(root);
  expect(textOf(root)).toContain('Research');
  app.unmount();
  vi.unstubAllGlobals();
});
