import { expect, test } from '@playwright/test';
import { bootstrapRecord } from './coresearch-files-fixture';

const reply = [
  '3. First item',
  '',
  '   ```python',
  '   print("<safe>")',
  '   ```',
  '',
  '   Continued within item',
  '',
  '4. Second item',
  '',
  '> Quote',
  '>',
  '> ```python',
  '> print(2)',
  '> ```',
  '>',
  '> After quote code',
  '',
  '<img src=x onerror=alert(1)> <script>alert(1)</script>',
  '',
  '[unsafe](javascript:alert(1)) [safe](https://example.test) ![remote](https://example.test/image.png)',
  '',
  '| Column |',
  '| --- |',
  '| Cell |',
].join('\n');

test('nested Markdown fences preserve list numbering, quote structure, copy and safe content', async ({
  page,
  baseURL,
}) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.addInitScript(() => localStorage.setItem('lykeion.workspace', 'lab'));
  await page.route(`${baseURL}/api/**`, async (route) => {
    const path = new URL(route.request().url()).pathname;
    let data: unknown = [];
    if (path === '/api/coresearch/bootstrap') data = bootstrapRecord();
    else if (path === '/api/me') data = { id: 'me', name: 'Ada', onboarded_at: '2026-01-01' };
    else if (path === '/api/workspaces') data = [{ id: 'lab', name: 'Fixture lab', role: 'owner' }];
    else if (path === '/api/workspaces/lab')
      data = { id: 'lab', name: 'Fixture lab', role: 'owner', event_cursor: 0 };
    else if (path === '/api/coresearch/projects/project')
      data = { id: 'project', name: 'Markdown regression', role: 'owner', archived_at: null };
    else if (path.endsWith('/chats'))
      data = [{ id: 'chat', project_id: 'project', owner_id: 'me', cli: 'claude' }];
    else if (path.endsWith('/messages'))
      data = [
        {
          id: 'turn',
          kind: 'turn',
          body: 'Show nested Markdown',
          agent_run_id: 'run',
          status: 'completed',
        },
        { id: 'reply', kind: 'reply', body: reply, agent_run_id: 'run' },
      ];
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) });
  });
  await page.goto('/#/coresearch/project/chat');
  const message = page.locator('.turn-reply .msg--assistant');
  await expect(message).toBeVisible();
  const list = message.locator('ol');
  await expect(list).toHaveAttribute('start', '3');
  const first = list.locator(':scope > li').first();
  await expect(first.locator('.code-block')).toHaveCount(1);
  await expect(list.locator(':scope > li')).toHaveCount(2);
  await expect(first.locator(':scope > p').last()).toHaveText('Continued within item');
  await expect(list.locator(':scope > li').last()).toHaveText('Second item');
  await expect(message.locator(':scope > li')).toHaveCount(0);
  const quote = message.locator(':scope > blockquote');
  await expect(quote.locator('.code-block')).toHaveCount(1);
  await expect(quote.locator(':scope > p').last()).toHaveText('After quote code');
  await first.getByRole('button', { name: 'Copy', exact: true }).click();
  await expect
    .poll(() => page.evaluate(() => navigator.clipboard.readText()))
    .toBe('print("<safe>")');
  await expect(first.locator('code')).toHaveText('print("<safe>")');
  await expect(message.locator('script, img, [onerror], a[href^="javascript:"]')).toHaveCount(0);
  await expect(message).toContainText('<img src=x onerror=alert(1)>');
  await expect(message).toContainText('[Image: remote]');
  await expect(message.getByRole('link', { name: 'safe', exact: true })).toHaveAttribute(
    'rel',
    'noopener noreferrer',
  );
  await expect(message.getByRole('region', { name: 'Table' }).locator('td')).toHaveText('Cell');
  expect(errors).toEqual([]);
});
