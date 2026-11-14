import { beforeEach, afterEach, expect, it, vi } from 'vitest';
import { h, nextTick, type Component } from 'vue';
import { createTreeApp, node, all, textOf } from '../../../../../tests/daemon-tree';
import ManuscriptFiles from './cowrite/ManuscriptFiles.vue';
import Outline from './cowrite/Outline.vue';
import SelectionToolbar from './cowrite/SelectionToolbar.vue';
import CowriteSidebar from './cowrite/CowriteSidebar.vue';
import { outlineOf } from './cowrite/lib/manuscript';
import {
  guardResourceDrafts,
  registerDraftVisit,
} from '../../state/resource-draft';
beforeEach(() => vi.stubGlobal('window', { addEventListener() {}, removeEventListener() {} }));
const cleanups: (() => void)[] = [];
afterEach(() => {
  cleanups.splice(0).forEach((f) => f());
  vi.unstubAllGlobals();
});
function mount(component: Component, props: Record<string, unknown> = {}) {
  const root = node('root');
  const app = createTreeApp({ render: () => h(component, props) });
  app.mount(root);
  cleanups.push(() => app.unmount());
  return root;
}
const click = (root: ReturnType<typeof node>, label: string) => {
  const button = all(root).find(
    (n) => n.tag === 'button' && (n.props['aria-label'] === label || textOf(n) === label),
  );
  expect(button, label).toBeTruthy();
  (button!.props.onClick as (e: object) => void)({});
  return nextTick();
};
it('puts main.tex first, groups folders and opens the selected file', async () => {
  const opened = vi.fn();
  const root = mount(ManuscriptFiles, {
    files: [
      { id: 's', path: 'sections/intro.tex' },
      { id: 'r', path: 'refs.bib' },
      { id: 'm', path: 'main.tex' },
    ],
    activePath: 'main.tex',
    onOpen: opened,
  });
  const names = all(root)
    .filter((n) => n.props.class === 'tsb-file-name')
    .map(textOf);
  expect(names).toEqual(['main.tex', 'refs.bib', 'intro.tex']);
  expect(all(root).find((n) => n.props['aria-current'] === 'true')?.props.title).toBe('main.tex');
  await click(root, 'intro.tex');
  expect(opened).toHaveBeenCalledWith('sections/intro.tex');
  await click(root, 'More — sections/intro.tex');
  expect(textOf(root)).toContain('Rename');
  expect(textOf(root)).toContain('Delete');
  await click(root, 'Delete');
  expect(textOf(root)).toContain('Remove file?');
  expect(all(root).some((n) => n.tag === 'button' && textOf(n).trim() === 'Remove')).toBe(true);
});
it('hides Outline for refs.bib and jumps to the exact section line', async () => {
  const jump = vi.fn();
  const root = mount(Outline, {
    entries: outlineOf('\\section{Introduction}\ntext\n\\subsection{Evidence}'),
    at: 2,
    onJump: jump,
  });
  expect(textOf(root)).toContain('Outline');
  await click(root, 'Evidence');
  expect(jump).toHaveBeenCalledWith(3);
  expect(textOf(mount(Outline, { entries: outlineOf('@article{key,title={Evidence}}') }))).toBe('');
});
it('renders only Compute before Files and Chats, and the Add a file menu', async () => {
  const root = mount(CowriteSidebar, { name: 'Paper', chats: [], tab: 'files' });
  expect(all(root).find((n) => n.props.role === 'tablist')?.props['aria-label']).toBe('Sidebar');
  expect(textOf(root).indexOf('Compute')).toBeLessThan(textOf(root).indexOf('Files'));
  await click(root, 'Add a file');
  expect(textOf(root)).toContain('New file');
  expect(textOf(root)).toContain('Upload');
  expect(textOf(root)).not.toContain("Research's Files");
});
it('shows project members without making the heading a button', () => {
  const root = mount(CowriteSidebar, {
    name: 'Paper', chats: [], owner: true,
    members: [{ id: '1', name: 'Ada Lovelace' }, { id: '2', name: 'Grace Hopper' }],
  });
  expect(textOf(root)).toContain('Ada Lovelace');
  expect(textOf(root)).toContain('Grace Hopper');
  expect(all(root).some((n) => n.tag === 'button' && textOf(n).trim() === 'Members')).toBe(false);
  expect(all(root).some((n) => n.tag === 'button' && n.props['aria-label'] === 'Manage members')).toBe(true);
});
it('offers Comment but no AI action to a member who does not own this chat', () => {
  vi.stubGlobal('window', { addEventListener() {}, removeEventListener() {} });
  const root = mount(SelectionToolbar, {
    owner: false,
    target: {
      rect: { top: 100, left: 100, width: 50, height: 20 },
      from: 0,
      to: 4,
      quote: 'text',
      path: 'main.tex',
      line: 1,
    },
    people: [],
  });
  expect(textOf(root)).toContain('Comment');
  expect(textOf(root)).not.toContain('Proofread');
  expect(textOf(root)).not.toContain('Edit with AI');
});
it('flushes collaborative drafts before navigation and retains the guard on failure', async () => {
  let dirty = true;
  let attempts = 0;
  const unregister = registerDraftVisit({
    identity: () => 'doc',
    dirty: () => dirty,
    discard: vi.fn(),
    flush: async () => {
      attempts++;
      if (attempts === 1) throw new Error('offline');
      dirty = false;
    },
  });
  cleanups.push(unregister);
  expect(await guardResourceDrafts()).toBe(false);
  expect(dirty).toBe(true);
  expect(await guardResourceDrafts()).toBe(true);
  expect(attempts).toBe(2);
});
