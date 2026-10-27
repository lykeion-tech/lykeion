import type { Page, WebSocketRoute } from '@playwright/test';
export const scriptText = 'print("original script v1")\n';
export const texts = [
  'time,residual\n1,0.02\n2,-0.01\n',
  'time,residual\n1,0.02\n2,-0.01\n',
  'time,residual\n1,9.99\n2,4.00\n',
];
export const version = (n: number, extra = {}) => ({
  file_id: 'f',
  n,
  size: Buffer.byteLength(texts[n - 1] ?? texts[0]!),
  sha256: n < 3 ? 'same' : 'different',
  created_at: `2026-10-26T12:00:0${n}Z`,
  author_user_id: null,
  run_id: n === 1 ? 'rid' : null,
  agent_run_id: null,
  overrode: null,
  restored_from: null,
  ...extra,
});
export const recorded = (extra = {}) => ({
  id: 'rid',
  key: 'R1',
  chat_id: 'producer',
  script_file_id: 'script',
  script_n: 1,
  inputs: [{ path: 'input.txt', file_id: 'input', n: 1, sha256: 'inputhash', size: 4 }],
  status: 'ok',
  env_sha: 'abcdef0123456789',
  facts: { stdout_tail: 'Baseline stable\n', stderr_tail: '' },
  rerun_of: null,
  finished_at: '2026-10-26T12:00:01Z',
  ...extra,
});
export async function installFilesFixture(
  page: Page,
  { user = 'me', head = 3 }: { user?: string; head?: number } = {},
) {
  let latest = head,
    eventId = 0,
    socket: WebSocketRoute | undefined;
  const writes: { path: string; body: string; workspace: string | undefined }[] = [],
    reads: string[] = [];
  const savedBytes = new Map<number, string>();
  const overrides = new Map<number, Record<string, unknown>>();
  const getVersion = (n: number) => version(n, overrides.get(n));
  const getBytes = (n: number) => savedBytes.get(n) ?? texts[n - 1] ?? 'remote head bytes';
  await page.addInitScript(() => localStorage.setItem('lykeion.workspace', 'lab'));
  await page.routeWebSocket('**/ws', (ws) => {
    socket = ws;
    ws.onMessage(() => {});
  });
  const files = () => [
    { id: 'f', path: 'residuals.csv', head: getVersion(latest) },
    { id: 'script', path: 'analysis.py', head: version(3) },
    {
      id: 'turnfile',
      path: 'notes.txt',
      head: version(1, { file_id: 'turnfile', run_id: null, agent_run_id: 'producing-turn' }),
    },
    { id: 'figure', path: 'figure.svg', head: version(1, { file_id: 'figure', run_id: null }) },
    { id: 'unknown', path: 'unknown.bin', head: version(1, { file_id: 'unknown', run_id: null }) },
  ];
  await page.route(
    (url) => url.pathname.startsWith('/api/'),
    async (route) => {
      const req = route.request(),
        path = new URL(req.url()).pathname;
      let data: unknown = [];
      if (req.method() !== 'GET') {
        writes.push({
          path,
          body: req.postData() ?? '',
          workspace: req.headers()['x-workspace-id'],
        });
        if (path.endsWith('/files')) {
          const body = req.postData() ?? '';
          const base = Number(body.match(/name="base"\r\n\r\n(\d+)/)?.[1]);
          const force = body.includes('name="force"\r\n\r\ntrue');
          if (base !== latest && !force) {
            await route.fulfill({
              status: 409,
              contentType: 'application/json',
              body: JSON.stringify({ error: 'stale_base', base, current: latest }),
            });
            return;
          }
          const boundary = req.headers()['content-type']?.split('boundary=')[1];
          const part = body.split(`--${boundary}`).find((p) => p.includes('name="file"')) ?? '';
          const bytes = part.slice(part.indexOf('\r\n\r\n') + 4).replace(/\r\n$/, '');
          const overrode = force ? latest : null;
          latest++;
          savedBytes.set(latest, bytes);
          overrides.set(latest, {
            author_user_id: user,
            run_id: null,
            agent_run_id: null,
            overrode,
            size: Buffer.byteLength(bytes),
            sha256: `draft-${latest}`,
          });
          data = getVersion(latest);
        } else if (path.endsWith('/restore')) {
          const { n, base } = req.postDataJSON();
          if (base !== latest) {
            await route.fulfill({
              status: 409,
              contentType: 'application/json',
              body: JSON.stringify({ error: 'stale_base', base, current: latest }),
            });
            return;
          }
          const original = getVersion(n),
            bytes = getBytes(n);
          latest++;
          savedBytes.set(latest, bytes);
          overrides.set(latest, {
            author_user_id: user,
            run_id: null,
            agent_run_id: null,
            restored_from: n,
            sha256: original.sha256,
            size: original.size,
          });
          data = getVersion(latest);
        } else data = recorded({ id: 'new', key: 'R4', status: 'queued' });
        await route.fulfill({
          status: 201,
          contentType: 'application/json',
          body: JSON.stringify(data),
        });
        return;
      }
      reads.push(path);
      if (path === '/api/me') data = { id: user, name: 'Ada Lovelace', onboarded_at: '2026-01-01' };
      else if (path === '/api/workspaces')
        data = [{ id: 'lab', name: 'Fixture lab', role: 'owner' }];
      else if (path === '/api/workspaces/lab')
        data = { id: 'lab', name: 'Fixture lab', role: 'owner', event_cursor: 0 };
      else if (path === '/api/workspaces/lab/members')
        data = [{ user_id: 'me', name: 'Ada Lovelace', role: 'owner' }];
      else if (path === '/api/coresearch/projects/p')
        data = {
          id: 'p',
          workspace_id: 'lab',
          name: 'Inspect the orbital residuals',
          python_version: '3.12.8',
          role: 'member',
          archived_at: null,
        };
      else if (path.endsWith('/chats'))
        data = [
          {
            id: 'selected',
            workspace_id: 'lab',
            project_id: 'p',
            owner_id: 'other',
            cli: 'claude',
            kernel_state: 'idle',
          },
          {
            id: 'producer',
            workspace_id: 'lab',
            project_id: 'p',
            owner_id: 'me',
            cli: 'claude',
            kernel_state: 'idle',
          },
        ];
      else if (path.endsWith('/files')) data = files();
      else if (path.endsWith('/files/f/versions'))
        data = Array.from({ length: latest }, (_, i) => getVersion(latest - i));
      else if (path.endsWith('/files/script/versions'))
        data = [
          version(3, { file_id: 'script', run_id: null }),
          version(2, { file_id: 'script', run_id: null }),
          version(1, { file_id: 'script', run_id: null }),
        ];
      else if (path.endsWith('/versions')) {
        const id = path.split('/').at(-2);
        data = [
          version(1, {
            file_id: id,
            run_id: null,
            agent_run_id: id === 'turnfile' ? 'producing-turn' : null,
          }),
        ];
      } else if (path.endsWith('/content')) {
        const n = Number(path.split('/').at(-2));
        const id = path.split('/').at(-4);
        const body =
          id === 'f'
            ? getBytes(n)
            : id === 'script'
              ? n < 3
                ? scriptText
                : 'print("new script head")\n'
              : id === 'turnfile'
                ? 'produced by exact turn'
                : id === 'figure'
                  ? '<svg xmlns="http://www.w3.org/2000/svg" width="40" height="30" onload="window.svgExecuted=true"><script>window.svgExecuted=true</script><rect width="40" height="30" fill="red"/></svg>'
                  : Buffer.from([0, 255, 1]);
        await route.fulfill({
          contentType: id === 'figure' ? 'image/svg+xml' : 'application/octet-stream',
          body,
        });
        return;
      } else if (path.endsWith('/runs'))
        data = [
          recorded(),
          recorded({ id: 'r2', key: 'R2', rerun_of: 'rid', facts: { matches_original: true } }),
          recorded({
            id: 'r3',
            key: 'R3',
            status: 'error',
            rerun_of: 'rid',
            facts: { matches_original: true },
          }),
        ];
      else if (path.endsWith('/runs/R1'))
        data = {
          ...recorded(),
          outputs: [{ file_id: 'f', n: 1, path: 'residuals.csv', sha256: 'same', size: 32 }],
        };
      else if (path.endsWith('/messages'))
        data = path.includes('/producer/')
          ? [
              {
                id: 'turn',
                chat_id: 'producer',
                kind: 'turn',
                body: 'Write the provenance note',
                agent_run_id: 'producing-turn',
                status: 'ok',
              },
              {
                id: 'reply',
                chat_id: 'producer',
                kind: 'reply',
                body: 'Saved the exact note.',
                agent_run_id: 'producing-turn',
              },
            ]
          : [
              {
                id: 'unrelated',
                chat_id: 'selected',
                kind: 'turn',
                body: 'Unrelated selected chat',
                agent_run_id: 'different',
                status: 'ok',
              },
            ];
      else if (path.endsWith('/steps'))
        data = [
          {
            seq: 1,
            attempt: 1,
            type: 'text',
            content: 'The producing chat tool transcript.',
            created_at: '2026-10-26',
          },
        ];
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) });
    },
  );
  return {
    writes,
    reads,
    remoteHead(n: number) {
      latest = n;
      socket?.send(
        JSON.stringify({
          type: 'event',
          event: {
            id: ++eventId,
            workspace_id: 'lab',
            type: 'coresearch.file_saved',
            payload: { project_id: 'p', file_id: 'f' },
          },
        }),
      );
    },
  };
}
export async function openSavedFile(page: Page, name: string) {
  await page.locator('.coresearch-sidebar-toggle').waitFor({ state: 'attached' });
  if (!(await page.getByRole('tablist', { name: 'Sidebar', exact: true }).isVisible())) {
    const toggle = page.getByRole('button', { name: 'Project sidebar', exact: true });
    if (await page.locator('.task-pane-scrim').isVisible())
      await page
        .locator('.task-rightpane')
        .getByRole('button', { name: 'Close panel', exact: true })
        .click();
    await toggle.click();
  }
  await page
    .getByRole('tablist', { name: 'Sidebar', exact: true })
    .getByRole('tab', { name: 'Files', exact: true })
    .click();
  await page.getByRole('button', { name, exact: true }).click();
}
