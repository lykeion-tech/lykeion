import { expect, test, type Page } from '@playwright/test';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { createHash } from 'node:crypto';

// This suite captures the independent browser renderer. It never attaches to the
// installed reference app and never copies reference records or seeds storage.
const output = resolve('research/visual-baseline/rewrite-browser');
const manifestPath = resolve(output, 'manifest.json');
const runId = process.env.LYKEION_VISUAL_RUN_ID || new Date().toISOString();
const sharedDifferences = [
  'Browser renderer at 1920×959 CSS pixels, DPR 1; not a native Electron window capture.',
  'Local workspace is Lykeion; the reference workspace/account and its user data are not copied.',
  'No agent runs, notifications, teammates, machines, secrets, or scientific outcomes are invented.',
  'Brand animation pose, current dates, generated IDs and scrollbar rendering can vary.',
  'Screenshots are observations for review, not assertions of pixel equality.',
];
type CaptureEntry = {
  state: string;
  reference: string | null;
  file: string;
  url: string;
  sha256: string;
  capturedAt: string;
  contextDifferences: string[];
};
const unavailableReferenceStates = [
  {
    references: ['45', '76', '77', '78', '79', '80'],
    reason:
      'Machine/runtime details require a registered runtime; this suite does not fabricate runtime receipts.',
  },
  {
    references: ['55', '56'],
    reason: 'Native application menu captures cannot be reproduced by a browser screenshot.',
  },
  {
    references: ['98'],
    reason: 'Run cost breakdown requires execution receipts; local drafts have no runs.',
  },
  {
    references: ['team-detail'],
    reason:
      'Team list drafts are reachable, but a distinct team detail surface is not implemented.',
  },
];
let entries: CaptureEntry[] = [];
function manifest() {
  return {
    runId,
    surface: 'browser',
    viewport: { width: 1920, height: 959, deviceScaleFactor: 1 },
    sharedDifferences,
    unavailableReferenceStates,
    captures: entries,
  };
}
test.beforeAll(async () => {
  await mkdir(output, { recursive: true });
  try {
    const previous = JSON.parse(await readFile(manifestPath, 'utf8'));
    if (previous.runId === runId) entries = previous.captures;
  } catch {
    // No usable manifest from a previous run; start a fresh one.
  }
  await writeFile(manifestPath, JSON.stringify(manifest(), null, 2));
});
async function capture(
  page: Page,
  state: string,
  differences: string[] = [],
  reference: string | null = state,
) {
  await page.evaluate(async () => {
    await document.fonts.ready;
    await new Promise<void>((resolve) =>
      requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
    );
  });
  const filename = `${state}.png`;
  const bytes = await page.screenshot({
    path: resolve(output, filename),
    animations: 'disabled',
    caret: 'hide',
  });
  entries = entries.filter((entry) => entry.state !== state);
  entries.push({
    state,
    reference,
    file: filename,
    url: page.url(),
    sha256: createHash('sha256').update(bytes).digest('hex'),
    capturedAt: new Date().toISOString(),
    contextDifferences: differences,
  });
  await writeFile(manifestPath, JSON.stringify(manifest(), null, 2));
}
async function start(page: Page) {
  await page.goto('/#/tasks');
  await expect(page.getByRole('heading', { name: 'Tasks', exact: true })).toBeVisible();
  await expect(page.locator('.board-empty')).toBeVisible();
}
async function navigate(page: Page, route: string) {
  // Navigate the renderer's public hash route without resetting its live tab session.
  await page.evaluate((value) => {
    location.hash = value;
  }, route);
  await expect.poll(() => new URL(page.url()).hash).toBe(`#${route}`);
  await expect(page.getByText('Loading workspace…', { exact: true })).toHaveCount(0);
}
async function dismissToast(page: Page) {
  const dismiss = page.getByRole('button', { name: 'Dismiss notification', exact: true });
  if (await dismiss.isVisible()) await dismiss.click();
}
async function closeDialog(page: Page) {
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toHaveCount(0);
}
async function draft(page: Page, title: string) {
  await page.locator('.quick-actions').getByRole('button', { name: 'New', exact: true }).click();
  await page.getByRole('button', { name: 'Switch to Manual', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByRole('textbox', { name: 'Task title', exact: true }).fill(title);
  await dialog
    .getByRole('textbox', { name: 'Description', exact: true })
    .fill('Disposable capture draft created through the UI. No experiment has run.');
  await dialog.getByRole('button', { name: 'Create Task' }).click();
  await expect(dialog).toHaveCount(0);
  await dismissToast(page);
  await expect(page.locator('.task-card').filter({ hasText: title })).toBeVisible();
}
const emptyRoutes: [string, string][] = [
  ['01-tasks', '/tasks'],
  ['04-researches', '/projects'],
  ['06-colleagues', '/colleagues'],
  ['07-autoresearch', '/autoresearch'],
  ['08-agents', '/agents'],
  ['09-new-agent', '/agents/new'],
  ['10-teams', '/squads'],
  ['11-skills', '/skills'],
  ['12-connectors', '/connectors'],
  ['13-machines', '/runtimes'],
  ['14-updates', '/updates'],
  ['15-settings', '/settings'],
  ['16-preferences', '/settings?tab=preferences'],
  ['17-inbox', '/inbox'],
  ['18-coresearch', '/chat'],
  ['35-general-settings', '/settings?tab=workspace'],
  ['36-notifications', '/settings?tab=notifications'],
  ['37-labels', '/settings?tab=labels'],
  ['38-properties', '/settings?tab=properties'],
  ['39-analytics', '/settings?tab=analytics'],
  ['api-connection', '/settings?tab=connection'],
  ['40-code', '/settings?tab=code'],
  ['44-agent-manual', '/agents/new/manual'],
  ['48-api-tokens', '/settings?tab=tokens'],
];

test('empty routes and first-install settings', async ({ page }) => {
  await start(page);
  for (const [state, route] of emptyRoutes) {
    await navigate(page, route);
    await capture(
      page,
      state,
      [
        'Fresh isolated context with no product records. Reference screenshots may contain existing account data.',
      ],
      state === 'api-connection' ? null : state,
    );
  }
  await navigate(page, '/chat');
  await page.getByRole('button', { name: 'New chat', exact: true }).click();
  await capture(page, '19-new-chat', [
    'No agents available; the composer shows the honest disconnected state.',
  ]);
  await navigate(page, '/autoresearch');
  await page.getByRole('button', { name: 'Task wakeups', exact: true }).click();
  await capture(page, '68-task-wakeups');
  await navigate(page, '/tasks');
  await page.getByRole('button', { name: 'Members', exact: true }).click();
  await capture(page, '42-tasks-empty-members');
  await page.locator('.task-toolbar').getByRole('button', { name: 'All', exact: true }).click();
  await page.getByRole('checkbox', { name: '0 agents working', exact: true }).click();
  await capture(page, '106-working-agent-filter');
});

test('create dialogs without saving product records', async ({ page }) => {
  await start(page);
  await page.locator('.quick-actions').getByRole('button', { name: 'New', exact: true }).click();
  await capture(page, '03-create-agent');
  await page.getByRole('button', { name: 'Switch to Manual', exact: true }).click();
  await capture(page, '30-create-manual');
  await closeDialog(page);
  const dialogs: [string, string, string][] = [
    ['05-new-research', '/projects', 'New research'],
    ['20-new-team', '/squads', 'New team'],
    ['21-add-connector', '/connectors', 'Add connector'],
    ['22-add-skill-menu', '/skills', 'Add skill'],
    ['24-new-autoresearch', '/autoresearch', 'New autoresearch'],
    ['25-add-computer', '/runtimes', 'Add a computer'],
  ];
  for (const [state, route, name] of dialogs) {
    await navigate(page, route);
    await page
      .locator('.resource-header-actions')
      .getByRole('button', { name, exact: true })
      .click();
    await expect(page.getByRole('dialog')).toBeVisible();
    await capture(page, state);
    if (state === '21-add-connector') {
      await page.getByRole('dialog').getByRole('button', { name: /HTTP/ }).click();
      await capture(page, '65-connector-http', [
        'Transport fields are unsaved; no credentials are entered.',
      ]);
      await page.getByRole('button', { name: 'Add header', exact: true }).click();
      await capture(page, '66-connector-header-row', ['Empty header row; no credentials entered.']);
      await page.getByRole('button', { name: 'JSON', exact: true }).click();
      await capture(page, '67-connector-json', ['Unsaved configuration; no remote connection.']);
    }
    if (state === '24-new-autoresearch') {
      const autoresearch = page.getByRole('dialog', { name: 'New autoresearch', exact: true });
      await autoresearch.getByRole('button', { name: 'At an interval', exact: true }).click();
      await autoresearch.getByLabel('Interval', { exact: true }).focus();
      await autoresearch.locator('.autoresearch-editor aside').evaluate((element) => {
        element.scrollTop = 308;
      });
      await capture(page, '70-autoresearch-interval', [
        'Inactive unsaved draft; next runs require the scheduling adapter.',
      ]);
      await autoresearch.getByLabel('Schedule days', { exact: true }).selectOption('week');
      await capture(page, '71-autoresearch-weekdays', [
        'Day selection only; no scheduler started.',
      ]);
      await autoresearch.getByRole('button', { name: 'Webhook', exact: true }).click();
      await autoresearch
        .getByLabel('Event filter values', { exact: true })
        .scrollIntoViewIfNeeded();
      await capture(page, '72-autoresearch-webhook', [
        'No webhook URL is fabricated or provisioned without a connected adapter.',
      ]);
    }
    if (state === '22-add-skill-menu') {
      await page.getByRole('button', { name: /Create manually Start from a blank/ }).click();
      await capture(page, '23-new-skill');
    }
    await closeDialog(page);
  }
  for (const [state, name] of [
    ['73-skill-url-import', 'Import from URL'],
    ['74-skill-local-import', 'Import from local'],
    ['75-skill-runtime-import', 'Copy from runtime'],
  ] as const) {
    await navigate(page, '/skills');
    await page
      .locator('.resource-header-actions')
      .getByRole('button', { name: 'Add skill', exact: true })
      .click();
    await page
      .getByRole('dialog')
      .getByRole('button', { name: new RegExp(name) })
      .click();
    await capture(page, state, [
      'Import adapter is unavailable; no external files or URLs were imported.',
    ]);
    await closeDialog(page);
  }
});

test('board menus, named view form, search and shell states', async ({ page }) => {
  await start(page);
  for (const [state, name] of [
    ['02-display', 'Display'],
    ['31-task-filter', 'Filter'],
    ['33-views', 'Views'],
    ['57-board-layout-menu', 'Board'],
  ] as const) {
    await page.locator('.task-toolbar').getByRole('button', { name, exact: true }).click();
    await capture(page, state, ['The board is empty because this context contains no tasks.']);
    if (state === '31-task-filter') {
      await page.getByRole('menuitem', { name: 'Status', exact: true }).hover();
      await capture(page, '32-status-filter');
      await page.keyboard.press('Escape');
    }
    if (state === '33-views') {
      await page.getByRole('menuitem', { name: 'New view', exact: true }).click();
      await capture(page, '34-new-view');
      await closeDialog(page);
    } else await page.keyboard.press('Escape');
  }
  await page.getByRole('button', { name: 'Search...', exact: true }).click();
  await capture(page, '27-search', ['No task results exist in this context.']);
  await closeDialog(page);
  await page.getByRole('button', { name: 'New tab', exact: true }).click();
  await capture(page, '28-new-tab');
  await page.getByRole('tab').last().click({ button: 'right' });
  await capture(page, '64-tab-context');
  await page.keyboard.press('Escape');
  await page
    .locator('.window-navigation')
    .getByRole('button', { name: 'Toggle left sidebar', exact: true })
    .click();
  await capture(page, '29-sidebar-collapsed');
  await page
    .locator('.window-navigation')
    .getByRole('button', { name: 'Toggle left sidebar', exact: true })
    .click();
  await page.getByRole('button', { name: 'Help', exact: true }).click();
  await capture(page, '53-help-menu');
  await page.keyboard.press('Escape');
  await page.locator('.workspace-switch').click();
  await capture(page, '54-workspace-menu', ['Local profile has no signed-in account identity.']);
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Open Aristotle', exact: true }).click();
  await capture(page, '41-aristotle', ['No running task or fabricated assistant transcript.']);
});

test('populated task detail and task auxiliary surfaces', async ({ page }) => {
  await start(page);
  await draft(page, 'Capture inquiry — reproducibility');
  await page.locator('.task-card').click();
  await expect(page.getByLabel('Scientific stage')).toHaveValue('Grounding');
  await capture(page, '26-task-detail', [
    'One disposable user-created local draft, Grounding, no execution transcript, costs, linked PRs or evidence.',
  ]);
  await page.getByRole('button', { name: 'Open task inspector', exact: true }).click();
  await capture(page, '91-task-inspector-files', [
    'No files, workspace provider, or execution artifacts available.',
  ]);
  await page.getByRole('tab', { name: 'Notebook', exact: true }).click();
  await capture(page, '92-task-inspector-notebook', [
    'No cells or kernel; execution adapter absent.',
  ]);
  await page.getByRole('button', { name: 'Set up environment', exact: true }).click();
  await capture(page, '93-task-inspector-compute', ['No connected machine/environment.']);
  await page.getByRole('button', { name: 'Close inspector', exact: true }).click();
  await page.getByRole('button', { name: 'New wakeup', exact: true }).click();
  await capture(page, '95-task-new-wakeup', ['Configuration draft only; no wakeup activated.']);
  await page.getByRole('button', { name: 'Choose a condition', exact: true }).click();
  await capture(page, '96-wakeup-conditions');
  await page.keyboard.press('Escape');
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Link pull request', exact: true }).click();
  await capture(page, '99-task-link-pr', ['Empty URL field; no reference PR metadata copied.']);
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Open timeline', exact: true }).click();
  await capture(page, '97-task-run-timeline', [
    'Intentional empty timeline: no run receipts exist. Reference plotted values are not copied.',
  ]);
  await closeDialog(page);
  await page.getByRole('button', { name: 'Add sub-tasks', exact: true }).click();
  await capture(page, '101-create-subtask', [
    'Parent points to the UI-created capture inquiry; no assignee inherited because none exists.',
  ]);
  await closeDialog(page);
  await page.getByRole('button', { name: 'Back to tasks', exact: true }).click();
  await page.locator('.task-card').click({ button: 'right' });
  await capture(page, '90-task-context');
  await page.getByRole('menuitem', { name: 'Pin to sidebar', exact: true }).click();
  await capture(page, '113-pinned-task', ['Only the local capture draft is pinned.']);
  await page.locator('.task-card').click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Unpin from sidebar', exact: true }).click();
  for (const mode of ['Table', 'Swimlane'] as const) {
    await page
      .locator('.task-toolbar')
      .getByRole('button', { name: mode === 'Table' ? 'Board' : 'Table', exact: true })
      .click();
    await page.getByRole('menuitemradio', { name: mode, exact: true }).click();
    await capture(page, mode === 'Table' ? '58-task-table' : '62-task-swimlane', [
      'One UI-created unassigned draft in Grounding.',
    ]);
    if (mode === 'Table')
      for (const [state, name] of [
        ['59-table-columns', 'Columns'],
        ['60-table-group', 'Group'],
      ] as const) {
        await page.getByRole('button', { name, exact: true }).click();
        await capture(page, state);
        await page.keyboard.press('Escape');
      }
    await page.getByRole('button', { name: 'Display', exact: true }).click();
    await capture(page, mode === 'Table' ? '61-table-display' : '63-swimlane-display');
    await page.keyboard.press('Escape');
  }
});

test('resource records are UI-created local drafts', async ({ page }) => {
  await start(page);
  await navigate(page, '/agents/new/manual');
  await page.getByPlaceholder('e.g. Deep Research Agent').fill('Capture methods reviewer');
  await page
    .getByPlaceholder('What does this agent do?')
    .fill('Disposable local configuration for visual verification.');
  await page
    .getByPlaceholder('Write what this agent should do, what to focus on, what to avoid…')
    .fill('Review methods only after an execution adapter is connected.');
  await page.getByRole('button', { name: 'Create & open agent', exact: true }).click();
  await expect(page.locator('.resource-detail')).toBeVisible();
  await dismissToast(page);
  await capture(page, '43-agent-detail', [
    'UI-created offline agent draft. No runtime, access grants, activity or runs are invented.',
  ]);
  for (const [state, name] of [
    ['81-agent-work', 'Work'],
    ['82-agent-capabilities', 'Capabilities'],
    ['85-agent-settings-general', 'Settings'],
  ] as const) {
    await page.locator('.resource-detail').getByRole('tab', { name, exact: true }).click();
    await capture(page, state, ['UI-created offline agent draft.']);
    if (name === 'Capabilities')
      for (const [state, section] of [
        ['83-agent-skills', 'Skills'],
        ['84-agent-connectors', 'Connectors'],
      ] as const) {
        await page
          .getByRole('navigation', { name: 'Agent configuration sections' })
          .getByRole('button', { name: section, exact: true })
          .click();
        await capture(page, state, ['No assigned skills or connectors.']);
      }
    if (name === 'Settings')
      for (const [state, section] of [
        ['86-agent-access', 'Access'],
        ['87-agent-environment', 'Environment'],
        ['88-agent-custom-args', 'Custom Args'],
      ] as const) {
        await page
          .getByRole('navigation', { name: 'Agent configuration sections' })
          .getByRole('button', { name: section, exact: true })
          .click();
        await capture(page, state, [
          'No access grants, runtime environment, or process invocation created.',
        ]);
      }
  }
  await navigate(page, '/squads');
  await page
    .locator('.resource-header-actions')
    .getByRole('button', { name: 'New team', exact: true })
    .click();
  await page.getByPlaceholder('e.g. Frontend Team').fill('Capture review team');
  await page
    .getByPlaceholder('Describe what this team is responsible for...')
    .fill('Disposable UI-created team draft.');
  await page.getByRole('dialog').getByRole('button', { name: 'Create Team', exact: true }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await dismissToast(page);
  await capture(
    page,
    '10-teams-created',
    [
      'One UI-created inert team draft. A distinct team detail route is not implemented; this capture records the available list.',
    ],
    '10-teams',
  );
  await navigate(page, '/projects');
  await page
    .locator('.resource-header-actions')
    .getByRole('button', { name: 'New research', exact: true })
    .click();
  await page
    .getByRole('textbox', { name: 'Project title', exact: true })
    .fill('Capture research project');
  await page
    .getByRole('textbox', { name: 'Project description', exact: true })
    .fill('Disposable empty research project.');
  await page.getByRole('button', { name: 'Create Project', exact: true }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await page
    .locator('.project-list')
    .getByRole('button', { name: /Capture research project/ })
    .click();
  await capture(
    page,
    'research-project-detail',
    [
      'UI-created empty research project. No corresponding populated reference project detail has been captured.',
    ],
    null,
  );
});

test('settings editor dialogs and observed theme choices', async ({ page }) => {
  await start(page);
  await navigate(page, '/settings?tab=labels');
  await page.getByRole('button', { name: 'New label', exact: true }).click();
  await capture(page, '102-new-label');
  await closeDialog(page);
  await navigate(page, '/settings?tab=properties');
  await page.getByRole('button', { name: 'New property', exact: true }).click();
  await capture(page, '104-new-property');
  await closeDialog(page);
  await navigate(page, '/settings?tab=preferences');
  for (const [state, name] of [
    ['50-theme-midnight', 'Midnight'],
    ['51-theme-dawn', 'Dawn'],
    ['52-theme-pale', 'Pale'],
  ] as const) {
    await page.getByRole('button', { name, exact: true }).click();
    await capture(page, state, [
      'Local appearance preference only; isolated context discarded after capture.',
    ]);
  }
  await page.getByRole('button', { name: 'Ash', exact: true }).click();
});

// Supplementary states use the same genuine UI flow and fresh isolated contexts.
test('agent suggestion, provider choices, avatar menus and actions', async ({ page }) => {
  await start(page);
  await navigate(page, '/agents/new/manual');
  await page.getByRole('button', { name: 'Add suggestion', exact: true }).click();
  await capture(page, '46-agent-suggestion', [
    'An empty unsaved suggestion; no source-account content.',
  ]);
  await page.getByRole('button', { name: 'Remove suggestion 1', exact: true }).click();
  await page.getByRole('button', { name: 'Model', exact: true }).click();
  await capture(page, '47-model-picker', [
    'No runtime-supplied model catalog; provider default remains available.',
  ]);
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: /^Thinking · / }).click();
  await capture(page, '118-thinking-levels', [
    'Configuration choices only; no model or run is active.',
  ]);
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Change avatar', exact: true }).click();
  await capture(page, '116-agent-avatar-picker', [
    'Default icon; no user avatar or uploaded image copied.',
  ]);
  await page.getByRole('button', { name: 'More emojis…', exact: true }).click();
  await expect(
    page.getByRole('button', { name: 'grinning face', exact: true }).first(),
  ).toBeVisible();
  await capture(page, '117-agent-emoji-browser', [
    'Emoji history is the independent picker default, not the reference user history.',
  ]);
  await page.keyboard.press('Escape');
  await page.getByPlaceholder('e.g. Deep Research Agent').fill('Capture configuration draft');
  await page.getByRole('button', { name: 'Create & open agent', exact: true }).click();
  await expect(page.locator('.resource-detail')).toBeVisible();
  await dismissToast(page);
  await page.getByRole('button', { name: 'Agent actions', exact: true }).click();
  await capture(page, '89-agent-actions', [
    'A UI-created offline configuration; actions requiring execution remain unavailable.',
  ]);
});

test('task property, pull request automation and wakeup task picker', async ({ page }) => {
  await start(page);
  await draft(page, 'Capture auxiliary inquiry');
  await page.locator('.task-card').click();
  await expect(page.getByLabel('Scientific stage')).toHaveValue('Grounding');
  await page.getByRole('button', { name: 'Add property', exact: true }).click();
  await capture(page, '94-task-add-property', [
    'The task is a local manual draft; no scientific result exists.',
  ]);
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Pull request automation', exact: true }).click();
  await capture(page, '100-task-pr-automation', [
    'No PR provider or merge automation; scientific gates remain required.',
  ]);
  await page.keyboard.press('Escape');
  await navigate(page, '/autoresearch');
  await page.getByRole('button', { name: 'Task wakeups', exact: true }).click();
  await page
    .locator('.resource-header-actions')
    .getByRole('button', { name: 'New wakeup', exact: true })
    .click();
  await expect(
    page.getByRole('dialog', { name: 'Choose task for wakeup', exact: true }),
  ).toBeVisible();
  await capture(page, '69-wakeup-task-picker', [
    'Blank search; only the disposable capture inquiry is eligible locally. No wakeup created.',
  ]);
});

test('manual creation more menu, calendar and relation pickers', async ({ page }) => {
  await start(page);
  await draft(page, 'Capture parent candidate');
  await page.getByRole('button', { name: 'New task in Grounding', exact: true }).click();
  await expect(page.getByRole('textbox', { name: 'Task title', exact: true })).toBeVisible();
  await capture(page, '107-column-add-task', [
    'Manual composer opened from Grounding; no automatic stage advancement.',
  ]);
  await page.getByRole('button', { name: 'More properties', exact: true }).click();
  await capture(page, '108-create-more-options');
  await page.getByRole('button', { name: 'Set due date...', exact: true }).click();
  await capture(page, '109-create-due-date', ['Calendar uses capture date; no deadline saved.']);
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'More properties', exact: true }).click();
  await page.getByRole('button', { name: 'Set parent task...', exact: true }).click();
  await capture(page, '114-create-parent-picker', [
    'Search starts blank; no parent relation saved.',
  ]);
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'More properties', exact: true }).click();
  await page.getByRole('button', { name: 'Add sub-task...', exact: true }).click();
  await capture(page, '115-create-subtask-picker', [
    'Search starts blank. Attaching existing children requires the relationship adapter; no attachment is performed.',
  ]);
});

test('settings picker states and task creation field preferences', async ({ page }) => {
  await start(page);
  await navigate(page, '/settings?tab=tokens');
  await page.getByRole('button', { name: 'Token expiry', exact: true }).click();
  await capture(page, '49-token-expiry', [
    'No token records copied; issuance unavailable without the token service.',
  ]);
  await page.keyboard.press('Escape');
  await navigate(page, '/settings?tab=labels');
  await page.getByRole('button', { name: 'New label', exact: true }).click();
  await page.getByRole('button', { name: 'Color', exact: true }).click();
  await capture(page, '103-label-color', ['Unsaved new label, independent default color.']);
  await page.keyboard.press('Escape');
  await closeDialog(page);
  await navigate(page, '/settings?tab=properties');
  await page.getByRole('button', { name: 'New property', exact: true }).click();
  await page.getByRole('combobox', { name: 'Type', exact: true }).click();
  await capture(page, '105-property-types', ['Custom property type popup; draft not saved.']);
  await page.keyboard.press('Escape');
  await closeDialog(page);
  await navigate(page, '/settings?tab=preferences');
  await page.locator('.field-table').scrollIntoViewIfNeeded();
  await capture(page, '110-create-field-preferences', [
    'First-install field choices; no reference preferences copied.',
  ]);
  await page
    .getByRole('combobox', { name: 'When replying to a running agent', exact: true })
    .click();
  await capture(page, '111-reply-mode-options', ['Custom reply menu; no running agent exists.']);
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Time zone', exact: true }).click();
  await capture(page, '112-timezone-picker', [
    'Capture environment uses Europe/Copenhagen; no user timezone setting copied.',
  ]);
});
