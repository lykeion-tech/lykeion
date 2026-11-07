import { expect, test } from '@playwright/test';
import { installAristotleFixture } from './aristotle-fixture';

test('opens Coresearch with the conversation unobstructed on a narrow screen', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await installAristotleFixture(page);
  await page.goto('/#/coresearch');
  await expect(page.locator('.application')).toHaveClass(/sidebar-hidden/);
  const input = page.getByRole('textbox', { name: 'Message the agent' });
  await expect(input).toBeVisible();
  await input.click();
  await expect(input).toBeFocused();
  await page.getByRole('button', { name: 'Toggle left sidebar' }).click();
  await expect(page.locator('.application')).not.toHaveClass(/sidebar-hidden/);
});

test('keeps workspace navigation reachable in a short viewport', async ({ page }) => {
  await page.setViewportSize({ width: 584, height: 400 });
  await installAristotleFixture(page);
  await page.goto('/#/studies');
  const navigation = page.locator('[aria-label="Workspace navigation"]');
  const links = navigation.locator('nav');
  await expect.poll(() => links.evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true);
  await links.getByRole('link', { name: 'Agents', exact: true }).scrollIntoViewIfNeeded();
  await expect(links.getByRole('link', { name: 'Agents', exact: true })).toBeInViewport();
  await expect(navigation.getByRole('link', { name: 'Settings' })).toBeInViewport();
});

test('returning to a conversation restores whether its experiment pane was open', async ({
  page,
}) => {
  await installAristotleFixture(page, true);
  await page.goto('/#/coresearch');
  await expect(page).toHaveURL(/coresearch\/conversations\/welcome$/);
  await page.getByRole('textbox', { name: 'Message the agent' }).fill('Test pane restoration');
  await page.getByRole('button', { name: 'Send', exact: true }).click();
  await expect(page).toHaveURL(/coresearch\/conversations\/accepted$/);
  await page.getByRole('button', { name: 'Open experiment surfaces' }).click();
  await expect(page.getByRole('heading', { name: 'Open a surface' })).toBeVisible();
  await page.goto('/#/studies');
  await page.goto('/#/coresearch/conversations/accepted');
  await expect(page.getByRole('heading', { name: 'Open a surface' })).toBeVisible();
  await page.getByRole('button', { name: 'Close pane' }).click();
  await page.goto('/#/studies');
  await page.goto('/#/coresearch/conversations/accepted');
  await expect(page.getByRole('button', { name: 'Open experiment surfaces' })).toBeVisible();
});

test('Back leaves the restored Coresearch conversation and Forward returns to it', async ({
  page,
}) => {
  await installAristotleFixture(page);
  await page.goto('/#/studies');
  await page.getByRole('link', { name: 'Coresearch', exact: true }).click();
  await expect(page).toHaveURL(/coresearch\/conversations\/welcome$/);
  await page.getByRole('button', { name: 'Go back' }).click();
  await expect(page).toHaveURL(/#\/studies$/);
  await page.getByRole('button', { name: 'Go forward' }).click();
  await expect(page).toHaveURL(/coresearch\/conversations\/welcome$/);
});

for (const [theme, surface, accent] of [
  ['dawn', 'rgb(42, 35, 44)', 'rgb(170, 66, 120)'],
  ['pale', 'rgb(42, 45, 64)', 'rgb(128, 84, 196)'],
] as const) {
  test(`${theme}: Coresearch uses the selected palette`, async ({ page }) => {
    await installAristotleFixture(page);
    await page.addInitScript(
      (theme) => localStorage.setItem('lykeion.preferences', JSON.stringify({ theme })),
      theme,
    );
    await page.goto('/#/coresearch');
    const coresearch = page.locator('.coresearch.aristotle-chat');
    await expect(coresearch).toHaveCSS('background-color', surface);
    await expect(coresearch.locator('.start-link').first()).toHaveCSS('color', accent);
  });
}

test('a new conversation can select an agent and use its starters', async ({ page }) => {
  const fixture = await installAristotleFixture(page, true);
  await page.route('**/api/agents', async (route) => {
    await route.fulfill({
      json: [
        {
          id: 'aristotle',
          name: 'Aristotle',
          owner_id: 'me',
          visibility: 'private',
          built_in_key: 'aristotle',
          archived_at: null,
          harness: 'claude',
          model: '',
          description: '',
          avatar_url: '',
          conversation_starters: [],
        },
        {
          id: 'analyst',
          name: 'Research analyst',
          owner_id: 'me',
          visibility: 'private',
          built_in_key: '',
          archived_at: null,
          harness: 'claude',
          model: '',
          description: 'Analyzes evidence',
          avatar_url: 'emoji:🧪',
          conversation_starters: [
            { label: 'Explore evidence', prompt: 'Examine these sources carefully.' },
          ],
        },
      ],
    });
  });
  await page.goto('/#/coresearch/new');
  await page.getByRole('button', { name: 'Choose agent' }).click();
  await page.getByRole('menuitemradio', { name: 'Research analyst' }).click();
  await expect(page.getByRole('heading', { name: 'Chat with Research analyst' })).toBeVisible();
  await expect(page.getByText('Analyzes evidence')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Customize starters' })).toHaveAttribute(
    'href',
    '#/agents/analyst?tab=settings',
  );
  await page.getByRole('button', { name: 'Explore evidence' }).click();
  await expect
    .poll(
      () => fixture.requests.find((item) => item.path === '/api/coresearch/conversations')?.body,
    )
    .toMatchObject({ agent_id: 'analyst', body: 'Examine these sources carefully.' });
});

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
    await page.getByRole('textbox', { name: 'Message the agent' }).fill('Retain my draft');
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
    await expect(page.getByRole('textbox', { name: 'Message the agent' })).toBeVisible();
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
    const input = page.getByRole('textbox', { name: 'Message the agent' });
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

for (const source of ['built_in', 'custom'] as const) {
  test(`conversation starters are visible and editable for ${source} agents`, async ({ page }) => {
    await installAristotleFixture(page);
    let agent = {
      id: 'aristotle',
      workspace_id: 'lab',
      owner_id: 'me',
      built_in_key: '',
      name: source === 'built_in' ? 'Theorist · Hypotheses Team' : 'Custom agent',
      description: 'Research',
      instructions: '',
      harness: 'claude',
      model: '',
      visibility: 'private',
      archived_at: null,
      conversation_starters: [] as { label: string; prompt: string }[],
      source,
    };
    await page.route('**/api/agents/aristotle', async (route) => {
      if (route.request().method() === 'PATCH')
        agent = { ...agent, ...route.request().postDataJSON() };
      await route.fulfill({ json: agent });
    });
    await page.route('**/api/agents', async (route) => {
      await route.fulfill({ json: [agent] });
    });
    await page.goto('/#/agents/aristotle?tab=settings');
    await expect(page.getByRole('heading', { name: 'Conversation starters' })).toBeVisible();
    await page.getByRole('button', { name: 'Add starter', exact: true }).click();
    await page.getByLabel('Starter 1 label').fill('Investigate');
    await page.getByLabel('Starter 1 prompt').fill('Investigate the evidence');
    await page.getByRole('button', { name: 'Save changes' }).click();
    await expect
      .poll(() => agent.conversation_starters)
      .toEqual([{ label: 'Investigate', prompt: 'Investigate the evidence' }]);
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
      .getByRole('cell', { name: 'Aristotle Research together' }),
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
  const input = page.getByRole('textbox', { name: 'Message the agent' }),
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
  await expect(page.getByRole('complementary', { name: 'Chat' })).toBeVisible();
  const conversations = page.getByRole('complementary', { name: 'Chat', exact: true });
  const settingsAppearance = async () =>
    page
      .getByRole('complementary')
      .getByRole('link', { name: 'Settings', exact: true })
      .evaluate((element) => {
        const rect = element.getBoundingClientRect();
        const style = getComputedStyle(element);
        const icon = element.querySelector('svg')!.getBoundingClientRect();
        return {
          rect: [rect.x, rect.y, rect.width, rect.height],
          icon: [icon.x, icon.y, icon.width, icon.height],
          font: [style.fontSize, style.fontWeight, style.lineHeight],
          padding: style.padding,
          color: style.color,
          background: style.backgroundColor,
        };
      });
  const chatSettings = await settingsAppearance();
  await expect(conversations.getByRole('link', { name: 'Projects', exact: true })).toHaveCount(0);
  await expect(
    conversations.getByRole('link', { name: 'Manage projects', exact: true }),
  ).toHaveCount(0);
  await page.getByRole('button', { name: 'Show navigation' }).click();
  const navigation = page.getByRole('complementary', { name: 'Workspace navigation' });
  await expect(navigation).toBeVisible();
  expect(await settingsAppearance()).toEqual(chatSettings);
  await expect(navigation.getByRole('link', { name: 'Projects', exact: true })).toHaveAttribute(
    'href',
    '#/projects',
  );
  await expect(page.getByRole('complementary', { name: 'Chat' })).toHaveCount(0);
  await page.reload();
  await expect(page.getByRole('button', { name: 'Show navigation' })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
  await page.getByRole('button', { name: 'Show conversations' }).click();
  await expect(page.getByRole('complementary', { name: 'Workspace navigation' })).toHaveCount(0);
  await page.getByRole('button', { name: 'Help', exact: true }).click();
  const helpMenu = page.getByRole('menu', { name: 'Help', exact: true });
  await expect(helpMenu).toBeVisible();
  await helpMenu.press('Escape');
  await expect(page.getByRole('button', { name: 'Help', exact: true })).toBeFocused();
  await page.getByRole('button', { name: 'Show navigation' }).click();
  await navigation.getByRole('link', { name: 'Projects', exact: true }).click();
  await expect(page).toHaveURL(/#\/projects$/);
  await page.goto('/#/coresearch/projects');
  await expect(page).toHaveURL(/coresearch\/projects$/);
  await page.goto('/#/coresearch/p/selected');
  await expect(page.getByRole('complementary', { name: 'Project sidebar' })).toHaveCount(0);
  await expect(navigation).toBeVisible();
  await page.getByRole('button', { name: 'Show conversations' }).click();
  await expect(navigation).toHaveCount(0);
  await expect(conversations).toHaveCount(1);
  await expect(conversations).toBeVisible();
});

test('lost acceptance response replays the same UUID and receipt without a second acceptance', async ({
  page,
}) => {
  const fixture = await installAristotleFixture(page, true);
  fixture.loseNextAcceptedResponse();
  await page.goto('/#/coresearch/new');
  const input = page.getByRole('textbox', { name: 'Message the agent' });
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

test('Aristotle portraits keep the full artwork inside every avatar', async ({ page }) => {
  await installAristotleFixture(page);
  const checkPortraits = async (selector: string) => {
    const portraits = page.locator(selector);
    await expect(portraits.first().locator('svg')).toBeVisible();
    for (const portrait of await portraits.all()) {
      await expect
        .poll(() =>
          portrait
            .locator('svg')
            .evaluate((element) => (element as SVGSVGElement).getBBox().height),
        )
        .toBeGreaterThan(0);
      const bounds = await portrait.evaluate((element) => {
        const svg = element.querySelector('svg')!;
        const box = svg.getBBox();
        const matrix = svg.getScreenCTM()!;
        const a = new DOMPoint(box.x, box.y).matrixTransform(matrix);
        const b = new DOMPoint(box.x + box.width, box.y + box.height).matrixTransform(matrix);
        const frame = element.getBoundingClientRect();
        return {
          fits:
            a.x >= frame.left - 0.5 &&
            a.y >= frame.top - 0.5 &&
            b.x <= frame.right + 0.5 &&
            b.y <= frame.bottom + 0.5,
          artwork: [a.x, a.y, b.x, b.y],
          frame: [frame.left, frame.top, frame.right, frame.bottom],
        };
      });
      expect.soft(bounds.fits, `${selector}: ${JSON.stringify(bounds)}`).toBe(true);
    }
  };
  await page.goto('/#/coresearch');
  await expect(page).toHaveURL(/conversations\/welcome$/);
  await checkPortraits('.aristotle-header .aristotle-art');
  await checkPortraits('.conversation-rail .aristotle-art');
  await expect(page.getByRole('link', { name: 'Chat', exact: true })).toHaveCSS(
    'font-weight',
    '400',
  );
  await page.getByRole('link', { name: 'New conversation', exact: true }).click();
  await checkPortraits('.aristotle-start .aristotle-art');
  await page.goto('/#/agents');
  await checkPortraits('.avatar .aristotle-art');
  await page.goto('/#/agents/aristotle');
  await checkPortraits('.agent-avatar .aristotle-art');
});

test('experiment pane header aligns with the conversation header', async ({ page }) => {
  await installAristotleFixture(page, true);
  await page.goto('/#/coresearch/new');
  await page.getByRole('textbox', { name: 'Message the agent' }).fill('Open the experiment');
  await page.getByRole('button', { name: 'Send', exact: true }).click();
  await expect(page).toHaveURL(/conversations\/accepted$/);
  await page.getByRole('button', { name: 'Open experiment surfaces' }).click();
  const conversation = await page.locator('.aristotle-header').boundingBox();
  const pane = await page.locator('.surface-pane .surface-header').boundingBox();
  expect(conversation).not.toBeNull();
  expect(pane).not.toBeNull();
  expect(
    Math.abs(conversation!.y + conversation!.height - pane!.y - pane!.height),
  ).toBeLessThanOrEqual(1);
});

test('empty Experiment surfaces align their icons and descriptions', async ({ page }) => {
  await installAristotleFixture(page, true);
  await page.route('**/api/coresearch/projects/private-project', (route) =>
    route.fulfill({
      json: {
        id: 'private-project',
        workspace_id: 'lab',
        name: 'Experiment',
        mode: 'experiment',
        archived_at: null,
      },
    }),
  );
  await page.route('**/api/coresearch/projects/private-project/chats', (route) =>
    route.fulfill({
      json: [
        {
          id: 'accepted-chat',
          project_id: 'private-project',
          workspace_id: 'lab',
          owner_id: 'me',
          cli: 'claude',
        },
      ],
    }),
  );
  await page.route('**/api/coresearch/projects/private-project/files', (route) =>
    route.fulfill({ json: [] }),
  );
  await page.route(
    '**/api/coresearch/projects/private-project/chats/accepted-chat/cells',
    (route) => route.fulfill({ json: [] }),
  );
  await page.route('**/api/coresearch/projects/private-project/compute', (route) =>
    route.fulfill({ json: { kernels: [], machines: [] } }),
  );
  await page.goto('/#/coresearch/new');
  await page.getByRole('textbox', { name: 'Message the agent' }).fill('Check surfaces');
  await page.getByRole('button', { name: 'Send', exact: true }).click();
  await expect(page).toHaveURL(/conversations\/accepted$/);
  await page.getByRole('button', { name: 'Open experiment surfaces' }).click();
  const positions = [];
  for (const [label, selector] of [
    ['Files', '.tsb-tasks-empty'],
    ['Notebook', '.nbp-empty'],
    ['Compute', '.compute-empty'],
  ]) {
    await page.getByLabel('Right pane').getByRole('button', { name: label!, exact: true }).click();
    const pane = page.locator('.task-rightpane-card');
    const icon = await pane.locator('.art-header .art-title svg').boundingBox();
    const text = await pane.locator(selector!).evaluate((element) => {
      const range = document.createRange();
      range.selectNodeContents(element.firstChild!);
      const rect = range.getBoundingClientRect();
      return { x: rect.x, y: rect.y };
    });
    positions.push({ iconX: icon!.x, x: text.x, y: text.y });
    await page.getByRole('button', { name: 'Back to surfaces' }).click();
  }
  for (const position of positions) {
    expect(Math.abs(position.x - position.iconX)).toBeLessThanOrEqual(1);
    expect(Math.abs(position.y - positions[0]!.y)).toBeLessThanOrEqual(1);
  }
});

test('Coresearch replies use readable and safe Markdown while a turn is active', async ({
  page,
  context,
}) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await installAristotleFixture(page, true);
  const body = [
    '## Findings',
    '',
    'A **clear** result with [a source](https://example.com).',
    '',
    '- First',
    '  - Nested',
    '- Second',
    '',
    '> Important context',
    '',
    '| Metric | Value |',
    '| --- | --- |',
    '| Mean | 3 |',
    '',
    '```python',
    'print(42)',
    '```',
    '',
    '<script>window.badMarkdown = true</script> [unsafe](javascript:alert(1))',
  ].join('\n');
  await page.route(
    '**/api/coresearch/projects/private-project/chats/accepted-chat/messages',
    (route) =>
      route.fulfill({
        json: [
          {
            id: 'turn',
            chat_id: 'accepted-chat',
            kind: 'turn',
            body: 'Question',
            agent_run_id: 'current-run',
            status: 'running',
          },
          {
            id: 'reply',
            chat_id: 'accepted-chat',
            kind: 'reply',
            body,
            agent_run_id: 'current-run',
          },
        ],
      }),
  );
  await page.goto('/#/coresearch/new');
  await page.getByRole('button', { name: 'Ground a research question' }).click();
  const reply = page.locator('.turn-reply .lyk-reading').last();
  await expect(reply.getByRole('heading', { name: 'Findings' })).toBeVisible();
  await expect(reply.locator('strong')).toHaveText('clear');
  await expect(reply.locator('ul ul li')).toHaveText('Nested');
  await expect(reply.locator('ul').first()).toHaveCSS('list-style-type', 'disc');
  await expect(reply.locator('blockquote')).toContainText('Important context');
  await expect(reply.getByRole('table')).toContainText('Mean');
  await expect(reply.locator('pre code')).toContainText('print(42)');
  await reply.locator('.code-block-copy').click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('print(42)');
  await expect(reply.locator('script,img,a[href^="javascript:"]')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Stop', exact: true })).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await reply.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
});
