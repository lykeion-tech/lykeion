import { expect, test } from '@playwright/test';
import { installAristotleFixture } from './aristotle-fixture';

for (const theme of ['ash', 'midnight']) {
  test(`${theme}: first welcome, unsaved draft and no-machine blocker`, async ({ page }) => {
    const fixture = await installAristotleFixture(page);
    await page.addInitScript(
      (theme) => localStorage.setItem('lykeion.preferences', JSON.stringify({ theme })),
      theme,
    );
    await page.goto('/#/coresearch');
    await expect(page).toHaveURL(/conversations\/welcome$/);
    await expect(page.getByText("Welcome to Coresearch. I'm Aristotle")).toBeVisible();
    await expect(page.locator('.welcome-actions button')).toHaveCount(3);
    await expect(page.locator('.aristotle-header .aristotle-art svg')).toHaveCount(1);
    await page.getByRole('link', { name: 'New conversation', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Chat with Aristotle' })).toBeVisible();
    await page.getByRole('textbox', { name: 'Message Aristotle' }).fill('Retain my draft');
    await expect(page.getByRole('button', { name: 'Send', exact: true })).toBeDisabled();
    await expect(
      page.getByText('No available own machine with claude', { exact: false }),
    ).toBeVisible();
    expect(fixture.requests).toEqual([]);
    await expect(page.getByRole('link', { name: 'Customize starters' })).toHaveAttribute(
      'href',
      '#/agents/aristotle?tab=settings',
    );
    await page.screenshot({ path: `/tmp/aristotle-${theme}-desktop.png` });
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.getByRole('textbox', { name: 'Message Aristotle' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Send', exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.screenshot({ path: `/tmp/aristotle-${theme}-narrow-open.png` });
    await page.getByRole('button', { name: 'Toggle left sidebar', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Chat with Aristotle' })).toBeVisible();
    await expect(
      page.getByRole('button', { name: 'Ground a research question', exact: true }),
    ).toBeVisible();
    const input = page.getByRole('textbox', { name: 'Message Aristotle' });
    await input.click();
    await expect(input).toBeFocused();
    await input.fill('Narrow viewport retained draft');
    await expect(input).toHaveValue('Narrow viewport retained draft');
    for (const target of [input, page.getByRole('button', { name: 'Send', exact: true })]) {
      expect(
        await target.evaluate((element) => {
          const r = element.getBoundingClientRect();
          return element.contains(document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2));
        }),
      ).toBe(true);
    }
    await page.screenshot({ path: `/tmp/aristotle-${theme}-narrow-closed.png` });
    for (const value of ['Single line draft', 'First line\nSecond line\nThird line\nFourth line']) {
      await input.fill(value);
      for (const fab of ['Show navigation', 'Show conversations']) {
        const textarea = await input.boundingBox();
        const button = await page.getByRole('button', { name: fab, exact: true }).boundingBox();
        expect(textarea).not.toBeNull();
        expect(button).not.toBeNull();
        const overlap =
          Math.max(
            0,
            Math.min(textarea!.x + textarea!.width, button!.x + button!.width) -
              Math.max(textarea!.x, button!.x),
          ) *
          Math.max(
            0,
            Math.min(textarea!.y + textarea!.height, button!.y + button!.height) -
              Math.max(textarea!.y, button!.y),
          );
        expect(overlap, `${fab} must not overlap editable text area`).toBe(0);
      }
    }
  });
}

test('starter settings deep-link saves and reflects in draft; Agents-first bootstrap', async ({
  page,
}) => {
  const fixture = await installAristotleFixture(page);
  await page.goto('/#/agents');
  await expect(
    page
      .getByRole('table', { name: 'Agents' })
      .getByRole('cell', { name: 'Aristotle You Research together' }),
  ).toBeVisible();
  await page.goto('/#/agents/aristotle?tab=settings');
  await page.getByRole('button', { name: 'Add starter', exact: true }).click();
  await page.getByRole('textbox', { name: 'Starter 1 label', exact: true }).fill('My evidence');
  await page
    .getByRole('textbox', { name: 'Starter 1 prompt', exact: true })
    .fill('Assess these sources carefully');
  await page.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect.poll(() => fixture.requests.length).toBe(1);
  expect(fixture.requests[0]!.body.conversation_starters).toEqual([
    { label: 'My evidence', prompt: 'Assess these sources carefully' },
  ]);
  await expect(page.getByText('Agent saved.', { exact: true })).toBeVisible();
  await page.goto('/#/coresearch');
  await expect(page.locator('.welcome-actions button')).toHaveCount(3);
  for (const label of [
    'Ground a research question',
    'Design a reproducible experiment',
    'Review evidence and findings',
  ]) {
    await expect(
      page.locator('.welcome-actions').getByRole('button', { name: label, exact: false }),
    ).toBeVisible();
  }
  await page.goto('/#/coresearch/new');
  await expect(page.getByRole('button', { name: 'My evidence' })).toBeVisible();
});

test('accepted IDs route to current transcript and Stop cancels current run', async ({ page }) => {
  const fixture = await installAristotleFixture(page, true);
  await page.goto('/#/coresearch/new');
  await page.getByRole('button', { name: 'Ground a research question' }).click();
  await expect(page).toHaveURL(/conversations\/accepted$/);
  await expect(page.getByText('Current fixture transcript', { exact: true })).toBeVisible();
  expect(fixture.requests[0]!.body.body).toContain('Help me turn my research question');
  expect(fixture.requests[0]!.body.request_id).toMatch(/^[\da-f-]{36}$/);
  expect(fixture.requests[0]!.workspace).toBe('lab');
  expect(
    await page.evaluate(() =>
      JSON.parse(localStorage.getItem('lykeion.aristotle.accepted:me:lab')!),
    ),
  ).toEqual({
    conversation: 'accepted',
    project: 'private-project',
    chat: 'accepted-chat',
    run: 'accepted-run',
  });
  await page.getByRole('button', { name: 'Stop', exact: true }).click();
  await expect
    .poll(() => fixture.requests.at(-1)?.path)
    .toBe('/api/coresearch/projects/private-project/chats/accepted-chat/turns/current-run/cancel');
  await expect(page.getByRole('button', { name: 'Stop', exact: true })).toHaveCount(0);
  await page.goto('/#/coresearch');
  await expect(page).toHaveURL(/conversations\/accepted$/);
  await page.reload();
  await expect(page.getByText('Current fixture transcript', { exact: true })).toBeVisible();
});

test('transport loss retains draft and retry UUID; edited payload allocates new UUID', async ({
  page,
}) => {
  const fixture = await installAristotleFixture(page, true);
  fixture.failNext(3);
  await page.goto('/#/coresearch/new');
  const input = page.getByRole('textbox', { name: 'Message Aristotle' }),
    send = page.getByRole('button', { name: 'Send', exact: true });
  await input.fill('Question');
  await send.click();
  await expect(send).toBeEnabled();
  await expect(input).toHaveValue('Question');
  await send.click();
  await expect.poll(() => fixture.requests.length).toBe(2);
  await expect(send).toBeEnabled();
  expect(fixture.requests[1]!.body.request_id).toBe(fixture.requests[0]!.body.request_id);
  await input.fill('Edited question');
  await send.click();
  await expect.poll(() => fixture.requests.length).toBe(3);
  expect(fixture.requests[2]!.body.request_id).not.toBe(fixture.requests[0]!.body.request_id);
});

test('stale and other-scope pointers fall back; rail switches persist without duplicate rails', async ({
  page,
}) => {
  await installAristotleFixture(page);
  await page.addInitScript(() => {
    localStorage.setItem('lykeion.aristotle.last:me:lab', 'deleted');
    localStorage.setItem('lykeion.aristotle.last:other:lab', 'foreign-member');
    localStorage.setItem('lykeion.aristotle.last:me:second', 'foreign-lab');
  });
  await page.goto('/#/coresearch');
  await expect(page).toHaveURL(/conversations\/welcome$/);
  await expect(page.getByRole('complementary', { name: 'Conversations' })).toBeVisible();
  const conversations = page.getByRole('complementary', { name: 'Conversations', exact: true });
  await expect(conversations.getByRole('link', { name: 'Projects', exact: true })).toHaveCount(0);
  await expect(
    conversations.getByRole('link', { name: 'Manage projects', exact: true }),
  ).toHaveCount(0);
  await page.getByRole('button', { name: 'Show navigation' }).click();
  const navigation = page.getByRole('complementary', { name: 'Workspace navigation' });
  await expect(navigation).toBeVisible();
  await expect(navigation.getByRole('link', { name: 'Projects', exact: true })).toHaveAttribute(
    'href',
    '#/projects',
  );
  await expect(page.getByRole('complementary', { name: 'Conversations' })).toHaveCount(0);
  await page.reload();
  await expect(page.getByRole('button', { name: 'Show navigation' })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
  await page.getByRole('button', { name: 'Show conversations' }).click();
  await expect(page.getByRole('complementary', { name: 'Workspace navigation' })).toHaveCount(0);
  await page.getByRole('button', { name: 'Show navigation' }).click();
  await navigation.getByRole('link', { name: 'Projects', exact: true }).click();
  await expect(page).toHaveURL(/#\/projects$/);
  await page.goto('/#/coresearch/projects');
  await expect(page).toHaveURL(/coresearch\/projects$/);
  await page.goto('/#/coresearch/p/selected');
  await expect(page.getByRole('complementary', { name: 'Project sidebar' })).toBeVisible();
});

test('lost acceptance response replays the same UUID and receipt without a second acceptance', async ({
  page,
}) => {
  const fixture = await installAristotleFixture(page, true);
  fixture.loseNextAcceptedResponse();
  await page.goto('/#/coresearch/new');
  const input = page.getByRole('textbox', { name: 'Message Aristotle' });
  const send = page.getByRole('button', { name: 'Send', exact: true });
  await input.fill('Already accepted on the server');
  await send.click();
  await expect(send).toBeEnabled();
  await expect(input).toHaveValue('Already accepted on the server');
  expect(fixture.receipts.size).toBe(1);
  await send.click();
  await expect(page).toHaveURL(/conversations\/accepted$/);
  expect(fixture.requests).toHaveLength(2);
  expect(fixture.requests[1]!.body).toEqual(fixture.requests[0]!.body);
  expect(fixture.receipts.size).toBe(1);
});
