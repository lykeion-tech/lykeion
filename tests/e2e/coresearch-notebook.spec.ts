import { expect, test } from '@playwright/test';

const png =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=';
const svg = Buffer.from(
  '<svg xmlns="http://www.w3.org/2000/svg" width="40" height="30" onload="window.svgExecuted=true"><script>window.svgExecuted=true</script><rect width="40" height="30" fill="red"/></svg>',
).toString('base64');
for (const owner of [true, false]) {
  test(`notebook and compute through the actual app: ${owner ? 'owner' : 'member'}`, async ({
    page,
  }) => {
    if (!owner) await page.setViewportSize({ width: 390, height: 844 });
    const errors: string[] = [],
      writes: { path: string; body: unknown; workspace: string | undefined }[] = [];
    page.on('pageerror', (e) => errors.push(e.message));
    await page.addInitScript(() => localStorage.setItem('lykeion.workspace', 'lab'));
    const cells = [
      {
        id: 'image',
        seq: 1,
        source: 'display(plot)',
        status: 'ok',
        execution_count: 1,
        outputs: [
          { type: 'image', mime: 'image/png', data: png },
          { type: 'image', mime: 'image/svg+xml', data: svg },
        ],
      },
      {
        id: 'error',
        seq: 2,
        source: 'raise ValueError("bad")',
        status: 'error',
        execution_count: 2,
        outputs: [{ type: 'error', traceback: ['Traceback', 'ValueError: bad'] }],
      },
      {
        id: 'blob',
        seq: 3,
        source: 'print("stored")',
        status: 'ok',
        execution_count: 3,
        outputs: null,
        outputs_blob: 'private/blob',
      },
      { id: 'queued', seq: 4, source: '1+1', status: 'queued', execution_count: null, outputs: [] },
    ].map((c) => ({ kernel: 'python3', outputs_blob: null, ...c }));
    await page.route(
      (url) => url.pathname.startsWith('/api/'),
      async (route) => {
        const req = route.request(),
          path = new URL(req.url()).pathname;
        if (req.method() !== 'GET') {
          writes.push({
            path,
            body: req.postDataJSON(),
            workspace: req.headers()['x-workspace-id'],
          });
          await route.fulfill({
            status: path.endsWith('/kernel') ? 204 : 201,
            contentType: 'application/json',
            body: path.endsWith('/kernel') ? '' : '{}',
          });
          return;
        }
        let data: unknown = [];
        if (path === '/api/me')
          data = {
            id: owner ? 'alice' : 'bob',
            name: 'Reader',
            email: 'reader@example.test',
            onboarded_at: '2026-01-01',
          };
        else if (path === '/api/workspaces')
          data = [{ id: 'lab', name: 'Fixture lab', slug: 'fixture', role: 'owner' }];
        else if (path === '/api/workspaces/lab')
          data = {
            id: 'lab',
            name: 'Fixture lab',
            slug: 'fixture',
            role: 'owner',
            event_cursor: 0,
          };
        else if (path === '/api/coresearch/projects/p')
          data = {
            id: 'p',
            workspace_id: 'lab',
            name: 'Residuals',
            role: 'member',
            python_version: '3.12.8',
            archived_at: null,
          };
        else if (path.endsWith('/chats'))
          data = [
            {
              id: 'c',
              project_id: 'p',
              workspace_id: 'lab',
              owner_id: 'alice',
              cli: 'claude',
              daemon_id: 'machine',
              kernel_state: 'busy',
              kernel_seen_at: '2026-10-26T12:00:00Z',
            },
          ];
        else if (path.endsWith('/cells')) data = cells;
        else if (path.endsWith('/cells/blob/outputs'))
          data = [{ type: 'text', text: 'stored output from member route' }];
        else if (path.endsWith('/environment'))
          data = {
            python_version: '3.12.8',
            setups: [
              {
                id: 'setup',
                created_at: '2026-10-26',
                status: 'failed',
                log: 'solver failed',
                conda: ['numpy'],
                python: '3.12.8',
              },
            ],
          };
        else if (path === '/api/runtimes')
          data = [
            {
              id: 'runtime',
              workspace_id: 'lab',
              owner_id: 'alice',
              daemon_id: 'machine',
              harness: 'claude',
              device_name: 'Alice private machine',
              visibility: 'private',
              status: 'offline',
              last_seen_at: '2026-10-26T11:00:00Z',
              active_runs: 99,
            },
          ];
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(data),
        });
      },
    );
    await page.goto('/#/coresearch/p/c');
    if (!owner) await page.getByRole('button', { name: 'Project sidebar', exact: true }).click();
    await page.getByRole('button', { name: 'Notebook', exact: true }).click();
    const notebook = page.getByTestId('notebook-panel');
    await expect(notebook.locator('.nbp-error')).toContainText('ValueError: bad');
    for (const summary of await notebook.locator('.nbp-output-summary').all()) {
      if ((await summary.getAttribute('aria-expanded')) === 'false') await summary.click();
    }
    await expect(notebook.locator('.nbp-image img')).toHaveCount(2);
    await expect(notebook.locator('.nbp-outputs svg')).toHaveCount(0);
    await expect(notebook).toContainText('stored output from member route');
    expect(await notebook.locator('.nbp-image img').nth(1).getAttribute('src')).toMatch(/^blob:/);
    await expect
      .poll(() =>
        notebook
          .locator('.nbp-image img')
          .nth(1)
          .evaluate((e: HTMLImageElement) => e.naturalWidth),
      )
      .toBe(40);
    expect(
      await page.evaluate(() => (window as unknown as { svgExecuted?: boolean }).svgExecuted),
    ).toBeUndefined();
    await expect(notebook).toContainText('Python 3.12.8');
    await expect(notebook).toContainText('solver failed');
    if (owner) {
      await notebook.getByRole('combobox', { name: 'Kernel', exact: true }).fill('custom-kernel');
      await notebook.getByRole('textbox', { name: 'Cell source', exact: true }).fill('print(2)');
      await notebook.getByRole('button', { name: 'Run', exact: true }).click();
      await expect(notebook.getByRole('textbox', { name: 'Cell source', exact: true })).toHaveValue(
        '',
      );
      await notebook.getByRole('button', { name: 'Interrupt', exact: true }).click();
      await notebook.getByRole('button', { name: 'Restart', exact: true }).click();
      await notebook
        .getByRole('textbox', { name: 'Setup Python version', exact: true })
        .fill('3.13');
      await notebook
        .getByRole('textbox', { name: 'Conda packages', exact: true })
        .fill('numpy\nscipy>=1.10');
      await notebook.getByRole('button', { name: 'Set up', exact: true }).click();
      await expect.poll(() => writes.length).toBe(4);
      expect(writes.map((w) => w.body)).toEqual([
        { kernel: 'custom-kernel', source: 'print(2)' },
        { op: 'interrupt' },
        { op: 'restart' },
        { chat_id: 'c', python: '3.13', conda: ['numpy', 'scipy>=1.10'] },
      ]);
      expect(writes.every((w) => w.workspace === 'lab')).toBe(true);
    } else {
      await expect(notebook.getByRole('textbox', { name: 'Cell source' })).toHaveCount(0);
      for (const name of ['Run', 'Set up', 'Interrupt', 'Restart'])
        await expect(notebook.getByRole('button', { name, exact: true })).toHaveCount(0);
    }
    await page.getByRole('tab', { name: 'Compute', exact: true }).click();
    const compute = page.getByTestId('compute-panel');
    for (const fact of [
      'Alice private machine',
      'offline',
      'busy',
      '2026-10-26T11:00:00Z',
      '2026-10-26T12:00:00Z',
      '1 queued cell',
    ])
      await expect(compute).toContainText(fact);
    await expect(compute).not.toContainText('99 queued');
    await expect(compute.getByRole('button', { name: 'Restart', exact: true })).toHaveCount(
      owner ? 1 : 0,
    );
    expect(errors).toEqual([]);
  });
}
