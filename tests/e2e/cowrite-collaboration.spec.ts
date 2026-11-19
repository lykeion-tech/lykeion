import { randomUUID } from 'node:crypto';
import { expect, test } from '@playwright/test';
import { labMember, labWorkspace } from './lab';

test('Cowrite saves a long inline comment and shows it live to a collaborator', async ({
  page,
  browser,
}) => {
  const ws = await labWorkspace(page.request);
  const headers = { 'X-Workspace-ID': ws };
  const peer = await labMember(browser, page.request, ws);
  try {
    expect(
      (await peer.page.request.patch('/api/me', { data: { name: 'Grace Collaborator' } })).status(),
    ).toBe(200);
    const seed = await page.request.post('/api/coresearch/conversations', {
      headers,
      data: {
        request_id: randomUUID(),
        mode: 'cowrite',
        template: 'blank',
        harness: 'claude',
        body: 'Shared synthetic manuscript',
      },
    });
    expect(seed.status(), await seed.text()).toBe(201);
    const { project_id, chat_id } = (await seed.json()).conversation;
    const path = '/api/coresearch/projects/' + project_id;
    expect(
      (
        await page.request.post(path + '/members', {
          headers,
          data: { user_id: peer.userId, role: 'member' },
        })
      ).status(),
    ).toBe(201);
    const route = '/#/coresearch/' + project_id + '/' + chat_id;
    await Promise.all([page.goto(route), peer.page.goto(route)]);
    await page.getByRole('button', { name: 'Tools', exact: true }).click();
    await page.getByRole('tab', { name: 'Comments', exact: true }).click();
    const word = 'Introduction';
    const box = await peer.page
      .locator('.cm-content .cm-line')
      .filter({ hasText: word })
      .first()
      .evaluate((line, word) => {
        const walker = document.createTreeWalker(line, NodeFilter.SHOW_TEXT);
        while (walker.nextNode()) {
          const node = walker.currentNode;
          const start = node.textContent?.indexOf(word) ?? -1;
          if (start < 0) continue;
          const range = document.createRange();
          range.setStart(node, start);
          range.setEnd(node, start + word.length);
          const rect = range.getBoundingClientRect();
          return { x: rect.x + rect.width / 2, y: rect.y + rect.height / 2 };
        }
        throw new Error('No test passage');
      }, word);
    await peer.page.mouse.dblclick(box.x, box.y);
    await peer.page.getByRole('button', { name: 'Comment', exact: true }).first().click();
    const comment = peer.page.getByRole('textbox', { name: 'Comment on the passage' });
    const message =
      'Check the units before comparing these measurements with the reference values.';
    await expect(comment).toBeFocused();
    await peer.page.keyboard.type(message, { delay: 10 });
    await expect(comment).toHaveValue(message);
    const saved = peer.page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        response.url().includes(path + '/docs/') &&
        response.url().endsWith('/comments'),
    );
    await peer.page.keyboard.press('Enter');
    expect((await saved).status()).toBe(201);
    const row = page.getByTestId('manuscript-comment').filter({ hasText: message });
    await expect(row.getByText(message, { exact: true })).toBeVisible();
    await expect(row).toContainText('Grace Collaborator');
  } finally {
    await peer.context.close();
  }
});
