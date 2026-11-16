import { type Page, type Route } from '@playwright/test';
import { ChangeSet, Text } from '@codemirror/state';
import { installFilesFixture, bootstrapRecord } from './coresearch-files-fixture.ts';
// Request bodies and change rows as the wire carries them: parsed JSON.
type Wire = ReturnType<typeof JSON.parse>;
export async function cowriteFixture(page: Page, owner = true) {
  let person = owner ? 'me' : 'member',
    expired = false;
  const base = await installFilesFixture(page, { user: owner ? 'me' : 'member' });
  await page.route('**/api/me', (r) =>
    r.fulfill({
      json: {
        id: person,
        name: 'Ada Lovelace',
        email: 'ada@example.com',
        onboarded_at: '2026-01-01',
        timezone: 'UTC',
        created_at: '2026-01-01T00:00:00Z',
        avatar_url: '',
        google_sub: null,
        career_stage: '',
      },
    }),
  );
  const docs = new Map([
    ['main', { text: '\\section{Introduction}\r\nA shared 😀 paragraph.\r\n', version: 0 }],
    ['intro', { text: '\\section{Evidence}\nSecond file.', version: 0 }],
    ['bib', { text: '@article{test,title={Example}}', version: 0 }],
  ]);
  const history = new Map<string, { version: number; client_id: string; changes: Wire }[]>();
  const writes: { path: string; body: Wire; at: number; person: string }[] = [];
  let conflicts = 0,
    force = false,
    offline = false,
    rejectTurn = false,
    activeTurn = false;
  const changes: Wire[] = [
    {
      id: 'pending',
      proposal_id: 'turn-proposal',
      created_at: '2026-10-28T10:00:00Z',
      file_id: 'main',
      from_pos: 24,
      to_pos: 25,
      removed_text: 'An',
      status: 'pending',
    },
    {
      id: 'stale',
      proposal_id: 'turn-proposal',
      created_at: '2026-10-28T10:00:00Z',
      file_id: 'main',
      from_pos: 26,
      to_pos: 32,
      removed_text: 'changed',
      status: 'stale',
    },
  ];
  let files = [
    { id: 'main', path: 'main.tex' },
    { id: 'intro', path: 'intro.tex' },
    { id: 'bib', path: 'refs.bib' },
  ].map((f) => ({ ...f, has_doc: true, head: { n: 1, size: 30, file_id: f.id } }));
  function apply(id: string, raw: Wire, client = 'remote') {
    const d = docs.get(id)!;
    const cs = ChangeSet.fromJSON(raw);
    d.text = cs.apply(Text.of(d.text.split('\n'))).toString();
    d.version++;
    const rows = history.get(id) ?? [];
    rows.push({ version: d.version, client_id: client, changes: raw });
    history.set(id, rows);
    for (const row of changes.filter((c) => c.file_id === id)) {
      row.from_pos = cs.mapPos(row.from_pos, 1);
      row.to_pos = Math.max(row.from_pos, cs.mapPos(row.to_pos, -1));
    }
  }
  const handler = async (route: Route) => {
    const req = route.request(),
      url = new URL(req.url()),
      path = url.pathname;
    let data: Wire = [];
    if (req.method() !== 'GET')
      writes.push({
        path,
        body: req.postData() ? req.postDataJSON() : undefined,
        at: Date.now(),
        person,
      });
    const match = path.match(/\/docs\/([^/]+)(?:\/(.*))?$/);
    if (path === '/api/coresearch/projects/p')
      data = {
        id: 'p',
        workspace_id: 'lab',
        name: 'Paper',
        mode: 'cowrite',
        role: owner ? 'owner' : 'member',
        archived_at: null,
      };
    else if (path.endsWith('/chats'))
      data = [
        { id: 'chat', owner_id: 'me', mode: 'cowrite', cli: 'claude', created_at: '2026-01-01' },
      ];
    else if (path.endsWith('/members'))
      data = [
        { user_id: 'me', role: 'owner' },
        { user_id: 'member', role: 'member' },
      ];
    else if (path.endsWith('/files')) data = files;
    else if (path.includes('/files/') && req.method() === 'PATCH') {
      const f = files.find((f) => f.id === path.split('/').at(-1));
      if (f) f.path = req.postDataJSON().path;
      data = f;
    } else if (path.endsWith('/messages') && activeTurn)
      data = [
        {
          id: 'turn-message',
          workspace_id: 'lab',
          chat_id: 'chat',
          kind: 'turn',
          author_user_id: 'me',
          agent_run_id: 'running-turn',
          body: 'Earlier request',
          created_at: '2026-10-28T10:00:00Z',
          status: 'running',
          failure_reason: null,
        },
      ];
    else if (path.endsWith('/turns') && rejectTurn) {
      await route.fulfill({ status: 503, json: { error: 'turn unavailable' } });
      return;
    } else if (path.endsWith('/compute')) data = { kernels: [], machines: [] };
    else if (match) {
      const [, id, suffix] = match;
      const d = docs.get(id!)!;
      if (!d) {
        await route.fulfill({ status: 404, json: { error: 'not found' } });
        return;
      }
      if (!suffix) data = d;
      else if (suffix === 'steps' && req.method() === 'GET')
        data = (history.get(id!) ?? [])
          .filter((s) => s.version > Number(url.searchParams.get('after')))
          .slice(0, 500);
      else if (suffix === 'steps') {
        if (offline) {
          await route.fulfill({ status: 503, json: { error: 'offline' } });
          return;
        }
        if (force) {
          force = false;
          apply(id!, ChangeSet.of({ from: 0, insert: 'remote ' }, d.text.length).toJSON());
        }
        const input = req.postDataJSON();
        if (input.base !== d.version) {
          conflicts++;
          await route.fulfill({ status: 409, json: { current: d.version } });
          return;
        }
        for (const raw of input.steps) apply(id!, raw, input.client_id);
        data = { version: d.version };
      } else if (suffix === 'changes') data = changes.filter((c) => c.file_id === id);
      else if (suffix === 'presence')
        data = [{ user_id: 'other', name: 'Grace', anchor: 3, head: 3 }];
      else if (suffix === 'cursor') {
        await route.fulfill({ status: 204 });
        return;
      } else if (suffix === 'comments') data = { id: 'comment' };
    } else if (path.includes('/changes/')) {
      const id = path.split('/').at(-2);
      const i = changes.findIndex((c) => c.id === id);
      if (i >= 0) changes.splice(i, 1);
      data = { id };
    }
    await route.fulfill({ json: data });
  };
  const matchProject = (url: URL) => url.pathname.startsWith('/api/coresearch/projects/p');
  await page.route(matchProject, handler);
  await page.route(
    (url) => url.pathname.startsWith('/api/') || url.pathname.startsWith('/auth/'),
    async (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path === '/auth/send-code') {
        await route.fulfill({ status: 204 });
        return;
      }
      if (path === '/auth/verify-code') {
        person = route.request().postDataJSON().email.startsWith('me@') ? 'me' : 'member';
        expired = false;
        offline = false;
        await route.fulfill({ status: 204 });
        return;
      }
      if (expired) {
        await route.fulfill({ status: 401, json: { error: 'session expired' } });
        return;
      }
      if (path === '/api/coresearch/bootstrap') {
        await route.fulfill({ json: bootstrapRecord(person) });
        return;
      }
      await route.fallback();
    },
  );
  return {
    ...base,
    expire() {
      expired = true;
      base.event('workspace.updated');
    },
    async attach(other: Page) {
      await installFilesFixture(other, { user: owner ? 'me' : 'member' });
      await other.route(matchProject, handler);
    },
    hideFile(id: string) {
      files = files.filter((f) => f.id !== id);
      docs.delete(id);
      base.remoteHead(3);
    },
    renameFile(id: string, path: string) {
      const f = files.find((f) => f.id === id);
      if (f) f.path = path;
      base.remoteHead(3);
    },
    rejectTurns(value: boolean) {
      rejectTurn = value;
    },
    setActiveTurn(value: boolean) {
      activeTurn = value;
    },
    docs,
    writes,
    changes,
    get conflicts() {
      return conflicts;
    },
    forceConflict() {
      force = true;
    },
    setOffline(v: boolean) {
      offline = v;
    },
    apply,
  };
}
