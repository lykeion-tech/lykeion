import { afterEach, expect, it, vi } from 'vitest';
import { createRenderer, h, nextTick, ref } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import StudyNotebook from './StudyNotebook.vue';
import type { StudyRecord } from '../../api/types';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';

const disposals: (() => void)[] = [];
afterEach(() => {
  disposals.splice(0).forEach((dispose) => dispose());
  vi.unstubAllGlobals();
});
function script(key = 'x1', extra: Partial<StudyRecord> = {}): StudyRecord {
  return {
    id: key,
    card_id: 'c',
    board_column: 'experimentation',
    round: 1,
    kind: 'run',
    role: 'agent',
    key,
    blob_key: '',
    agent_run_id: 'run-1',
    superseded_at: null,
    created_at: '2026-11-03T10:00:00Z',
    body: {
      tool: 'run_script',
      language: 'python',
      path: `${key}.py`,
      script: 'print(1)',
      output: `output ${key}`,
      exit_status: 0,
    },
    ...extra,
  };
}
function setup(records: StudyRecord[] | null = [], runId: string | null = null) {
  workspaceId.value = 'w';
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  if (records !== null) client.setQueryData([...keys.recordsAll('w', 'c'), 'notebook'], records);
  client.setQueryData(keys.runs('w', 'c'), [
    { id: 'run-1', agent_name: 'Ada', role: 'leader', board_column: 'experimentation', round: 1 },
  ]);
  const id = ref('c'),
    selectedRun = ref(runId),
    root = node('root');
  const app = createTreeApp({
    render: () => h(StudyNotebook, { cardId: id.value, runId: selectedRun.value }),
  });
  app.use(VueQueryPlugin, { queryClient: client });
  app.mount(root);
  disposals.push(() => {
    app.unmount();
    client.clear();
  });
  return { client, root, id, selectedRun };
}
const cells = (root: ReturnType<typeof node>) => all(root).filter((n) => n.tag === 'article');
const button = (root: ReturnType<typeof node>, label: string) =>
  all(root).find((n) => n.tag === 'button' && textOf(n) === label)!;
it('shows interactive agent code for Python, R and Julia', async () => {
  const { root } = setup(
    ['python', 'r', 'julia'].map((language, i) =>
      script(`interactive-${i}`, {
        body: {
          tool: 'run_code',
          language,
          code: `example_${language}()`,
          output: 'ok',
          exit_status: 0,
        },
      }),
    ),
  );
  await nextTick();
  expect(cells(root)).toHaveLength(3);
  for (const language of ['python', 'r', 'julia'])
    expect(textOf(root)).toContain(`example_${language}()`);
});
it('shows one read-only cell per recorded script, including superseded cells', async () => {
  const { root } = setup([script('x1', { superseded_at: '2026-11-03T11:00:00Z' }), script('x2')]);
  await nextTick();
  expect(cells(root)).toHaveLength(2);
  expect(textOf(root)).toContain('Ada · Experimentation · try 1');
  expect(all(root).filter((n) => n.tag === 'h3')).toHaveLength(0);
  for (const key of ['x1', 'x2']) {
    expect(textOf(root)).toContain(`${key}.py`);
    expect(textOf(root)).toContain(`output ${key}`);
  }
  expect(all(root).filter((n) => ['input', 'textarea'].includes(n.tag))).toHaveLength(0);
  expect(all(root).filter((n) => n.tag === 'button' && /run/i.test(textOf(n)))).toHaveLength(0);
});
it('marks a failed script and shows its recorded stderr', async () => {
  const { root } = setup([
    script('x1', {
      body: { tool: 'run_script', script: 'bad()', exit_status: 1, error: 'NameError: bad' },
    }),
  ]);
  await nextTick();
  expect(textOf(root)).toContain('✗ exit 1');
  expect(textOf(root)).toContain('NameError: bad');
});
it('folds long output and expands the exact recorded lines', async () => {
  const output = Array.from({ length: 30 }, (_, i) => `line ${i + 1}`).join('\n') + '\n';
  const { root } = setup([
    script('x1', { body: { tool: 'run_script', script: 'print(lines)', output } }),
  ]);
  await nextTick();
  const block = () => all(root).find((n) => n.props['aria-label'] === 'Printed output')!;
  expect(textOf(block()).split('\n')).toHaveLength(12);
  expect(textOf(block())).not.toContain('line 13');
  (button(root, 'show all 30 lines').props.onClick as () => void)();
  await nextTick();
  expect(textOf(block())).toBe(output);
});
it('groups cells by board order and then try, preserving chronology within each try', async () => {
  const { root } = setup([
    script('v', { board_column: 'validation' }),
    script('e2', { round: 2 }),
    script('e1'),
    script('g', { board_column: 'grounding' }),
  ]);
  await nextTick();
  expect(
    cells(root)
      .map(textOf)
      .map((text) => text.match(/[vegx]\d?\.py/)?.[0]),
  ).toEqual(['g.py', 'e1.py', 'e2.py', 'v.py']);
});
it('says when nothing was recorded', async () => {
  const { root } = setup();
  await nextTick();
  expect(textOf(root)).toContain('No scripts were recorded in this study yet.');
});
it('reads all four stage histories and refreshes them on record invalidation', async () => {
  let later = false;
  const fetch = vi.fn(async (path: string) => {
    const url = new URL(path, 'http://localhost');
    if (url.pathname !== '/api/cards/c/records' || url.searchParams.get('all') !== '1')
      return Response.json({ error: 'history query required' }, { status: 400 });
    return Response.json(
      url.searchParams.get('column') === 'experimentation'
        ? [
            script('old', { superseded_at: '2026-11-03T11:00:00Z' }),
            ...(later ? [script('new')] : []),
          ]
        : [],
    );
  });
  vi.stubGlobal('fetch', fetch);
  const { root, client } = setup(null);
  await vi.waitFor(() => expect(cells(root)).toHaveLength(1));
  expect(fetch.mock.calls.map(([path]) => path).sort()).toEqual([
    '/api/cards/c/records?column=experimentation&all=1',
    '/api/cards/c/records?column=grounding&all=1',
    '/api/cards/c/records?column=hypotheses&all=1',
    '/api/cards/c/records?column=validation&all=1',
  ]);
  later = true;
  await client.invalidateQueries({ queryKey: keys.recordsAll('w', 'c') });
  await vi.waitFor(() => expect(cells(root)).toHaveLength(2));
  expect(textOf(root)).toContain('old.py');
  expect(textOf(root)).toContain('new.py');
});
it('shows a history failure and retries before declaring an empty notebook', async () => {
  let fails = true;
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      fails ? Response.json({ error: 'History unavailable' }, { status: 500 }) : Response.json([]),
    ),
  );
  const { root } = setup(null);
  await nextTick();
  expect(textOf(root)).toContain('Loading notebook');
  await vi.waitFor(() => expect(textOf(root)).toContain('History unavailable'));
  expect(textOf(root)).not.toContain('No scripts were recorded');
  fails = false;
  (button(root, 'Retry').props.onClick as () => void)();
  await vi.waitFor(() => expect(textOf(root)).toContain('No scripts were recorded'));
});
it('marks shortened stored output without claiming to have its unrecorded remainder', async () => {
  const { root } = setup([
    script('x1', {
      body: {
        tool: 'run_script',
        script: 'print(1)',
        output: 'partial',
        truncated: true,
        error: 'error part',
        error_truncated: true,
      },
    }),
  ]);
  await nextTick();
  expect(textOf(root)).toContain('Recorded output was truncated');
  expect(textOf(root)).toContain('Recorded error was truncated');
});

it('keeps the normal opening position for orphan cells, including after history refreshes', async () => {
  const { root, client } = setup([script('orphan', { agent_run_id: null })]);
  const scroll = vi.fn();
  Object.assign(cells(root)[0]!, { scrollIntoView: scroll });
  await nextTick();
  await nextTick();
  expect(scroll).not.toHaveBeenCalled();
  client.setQueryData(
    [...keys.recordsAll('w', 'c'), 'notebook'],
    [script('orphan', { agent_run_id: null }), script('later')],
  );
  await nextTick();
  await nextTick();
  expect(scroll).not.toHaveBeenCalled();
});

it('scrolls to the first cell for a run, including after history arrives', async () => {
  const { root, selectedRun, client } = setup([script('x1'), script('x2')]);
  await nextTick();
  const first = vi.fn(),
    second = vi.fn();
  Object.assign(cells(root)[0]!, { scrollIntoView: first });
  Object.assign(cells(root)[1]!, { scrollIntoView: second });
  selectedRun.value = 'run-1';
  await nextTick();
  await nextTick();
  expect(first).toHaveBeenCalledOnce();
  expect(second).not.toHaveBeenCalled();
  client.setQueryData(
    [...keys.recordsAll('w', 'c'), 'notebook'],
    [script('x1'), script('x2'), script('x3')],
  );
  await nextTick();
  await nextTick();
  expect(first).toHaveBeenCalledOnce();
  selectedRun.value = 'run-2';
  await nextTick();
  await nextTick();
  client.setQueryData(
    [...keys.recordsAll('w', 'c'), 'notebook'],
    [script('x1', { agent_run_id: 'run-2' }), script('x2')],
  );
  await vi.waitFor(() => expect(first).toHaveBeenCalledTimes(2));
});

it('opens the Notebook from the study pane and can jump to the same run again', async () => {
  const { default: StudyDetail } = await import('./StudyDetail.vue');
  vi.stubGlobal('window', { innerWidth: 1500, addEventListener() {}, removeEventListener() {} });
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    },
  );
  const renderer = createRenderer<ReturnType<typeof node>, ReturnType<typeof node>>({
    createElement: (tag) =>
      Object.assign(node(tag), { getBoundingClientRect: () => ({ width: 1000 }) }),
    createText: (text) => node('#text', text),
    createComment: () => node('#comment'),
    setText: (n, text) => {
      n.text = text;
    },
    setElementText: (n, text) => {
      n.children = [];
      n.text = text;
    },
    insert: (child, parent, anchor) => {
      child.parent?.children.splice(child.parent.children.indexOf(child), 1);
      child.parent = parent;
      const i = anchor ? parent.children.indexOf(anchor) : -1;
      if (i < 0) parent.children.push(child);
      else parent.children.splice(i, 0, child);
    },
    remove: (child) => {
      child.parent?.children.splice(child.parent.children.indexOf(child), 1);
    },
    parentNode: (n) => n.parent,
    nextSibling: (n) => n.parent?.children[n.parent.children.indexOf(n) + 1] ?? null,
    patchProp: (n, key, _prev, next) => {
      n.props[key] = next;
    },
  });
  workspaceId.value = 'w';
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, enabled: false, staleTime: Infinity } },
  });
  client.setQueryData(keys.card('w', 'c'), {
    id: 'c',
    title: 'Notebook study',
    question: 'A study',
    board_column: 'experimentation',
    state: 'working',
    flag: '',
    ending: '',
    teams: {},
    attachments: [],
  });
  client.setQueryData(keys.runs('w', 'c'), []);
  client.setQueryData([...keys.recordsAll('w', 'c'), 'notebook'], [script()]);
  const detail = ref<{ openNotebookAt: (run: string) => void }>();
  const root = node('root');
  const app = renderer.createApp({
    render: () =>
      h(StudyDetail, {
        id: 'c',
        ref: detail,
      }),
  });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: { render: () => null } }],
  });
  await router.push('/');
  app.use(router);
  app.use(VueQueryPlugin, { queryClient: client });
  app.mount(root);
  disposals.push(() => {
    app.unmount();
    client.clear();
  });
  await nextTick();
  expect(
    all(root)
      .filter((n) => n.props.class === 'surface-choice')
      .map(textOf),
  ).toEqual(['Timeline', 'Review', 'Notebook', 'Files', 'Properties']);
  expect(detail.value?.openNotebookAt).toBeTypeOf('function');
  detail.value!.openNotebookAt('run-1');
  await nextTick();
  await nextTick();
  expect(all(root).some((n) => n.props['aria-label'] === 'Notebook')).toBe(true);
  const scroll = vi.fn();
  Object.assign(cells(root)[0]!, { scrollIntoView: scroll });
  detail.value!.openNotebookAt('run-1');
  await nextTick();
  await nextTick();
  expect(scroll).toHaveBeenCalledOnce();
});
