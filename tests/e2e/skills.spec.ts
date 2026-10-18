import { expect, test, type Page } from '@playwright/test';
import { labWorkspace, labSignIn, LAB, labSkillFolder } from './lab';
import { rmSync } from 'node:fs';
import type { SkillSummary, SkillFileMeta } from '../../apps/dashboard/src/api/types';

const text = (name: string) =>
  `---\nname: ${name}\ndescription: Review a scientific result.\n---\n\n# Review a scientific result\n\nCheck the recorded evidence.\n`;
async function skill(page: Page, name: string, files = false) {
  const ws = await labWorkspace(page.request);
  const headers = { 'X-Workspace-ID': ws };
  let response;
  if (files)
    response = await page.request.post('/api/skills/import', {
      headers,
      multipart: {
        file: { name: 'whole.zip', mimeType: 'application/zip', buffer: await archive(name) },
      },
    });
  else
    response = await page.request.post('/api/skills', {
      headers,
      data: { name, description: 'Review a scientific result.' },
    });
  expect(response.status()).toBe(201);
  return { ws, headers, detail: await response.json() };
}
async function open(page: Page) {
  await page.goto('/#/settings/skills');
  await expect(page.getByRole('heading', { name: 'Skills', exact: true })).toBeVisible();
}

test('skills list opens modal C and preserves search', async ({ page }) => {
  const name = `review-helper-${Date.now()}`;
  await skill(page, name, true);
  await open(page);
  await page.getByRole('searchbox', { name: 'Search skills' }).fill(name);
  await expect(page.getByRole('button', { name: 'All', exact: true })).toHaveCount(0);
  await expect(page.getByLabel('Current scope: All')).toHaveText('All');
  await expect(page.getByRole('button', { name, exact: true })).toBeVisible();
  await page.screenshot({ path: 'test-results/skills/list-filtered.png' });
  await page.getByRole('button', { name, exact: true }).click();
  const dialog = page.getByRole('dialog', { name });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText('Contains scripts', { exact: true })).toBeVisible();
  await expect(dialog.getByText('You', { exact: true })).toBeVisible();
  await dialog.getByRole('tab', { name: 'Files', exact: true }).click();
  await expect(
    dialog.getByRole('navigation', { name: 'Skill files' }).getByRole('button').first(),
  ).toHaveText('SKILL.md');
  await dialog.getByRole('button', { name: 'scripts/check.py', exact: true }).click();
  await expect(dialog.locator('.file-preview')).toContainText('never executed');
  await dialog.getByRole('button', { name: 'references/raw.bin', exact: true }).click();
  await expect(dialog.getByText('Binary file', { exact: true })).toBeVisible();
  await page.screenshot({ path: 'test-results/skills/files-binary.png' });
  await expect(dialog.getByRole('textbox', { name: 'SKILL.md' })).toHaveCount(0);
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole('searchbox', { name: 'Search skills' })).toHaveValue(name);
  await expect(page.getByRole('button', { name, exact: true })).toBeFocused();
});

test('manual creation opens SKILL.md editor', async ({ page }) => {
  await open(page);
  await page.getByRole('button', { name: 'Add skill', exact: true }).click();
  await page.getByRole('button', { name: 'Create manually', exact: false }).click();
  await page.screenshot({ path: 'test-results/skills/manual-form.png' });
  const name = `manual-${Date.now()}`;
  await page.getByRole('textbox', { name: 'Name', exact: true }).fill(name);
  await page.getByRole('textbox', { name: 'Description', exact: true }).fill('A manual skill');
  await page.getByRole('button', { name: 'Create skill', exact: true }).click();
  const dialog = page.getByRole('dialog', { name });
  const editor = dialog.getByRole('textbox', { name: 'SKILL.md', exact: true });
  await expect(editor).toBeVisible();
  await editor.fill(text(name) + '\nSaved in browser');
  await dialog.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(editor).toHaveCount(0);
  await expect(dialog).toContainText('Saved in browser');
});

test('local and URL imports preserve all files', async ({ page }) => {
  const name = `local-${Date.now()}`;
  await open(page);
  await page.getByRole('button', { name: 'Add skill', exact: true }).click();
  await page.getByRole('button', { name: 'Import from local', exact: false }).click();
  await page
    .locator('input[type=file][data-archive]')
    .setInputFiles({ name: 'whole.zip', mimeType: 'application/zip', buffer: await archive(name) });
  await page.getByRole('button', { name: 'Import', exact: true }).click();
  await expect(page.getByRole('dialog', { name })).toBeVisible();
  await page.getByRole('button', { name: 'Close skill', exact: true }).click();
  const ws = await labWorkspace(page.request);
  const list = await (
    await page.request.get('/api/skills', { headers: { 'X-Workspace-ID': ws } })
  ).json();
  const id = list.find((x: SkillSummary) => x.name === name).id;
  const bytes = await (
    await page.request.get(`/api/skills/${id}/files?path=references%2Fraw.bin`, {
      headers: { 'X-Workspace-ID': ws },
    })
  ).body();
  expect(bytes).toEqual(Buffer.from([0, 255, 128, 13, 10]));
  await page.getByRole('button', { name: 'Add skill', exact: true }).click();
  await page.getByRole('button', { name: 'Import from URL', exact: false }).click();
  await page
    .getByRole('textbox', { name: 'Skill URL' })
    .fill('https://github.com/ui2-fixture/published');
  await page.getByRole('button', { name: 'Import', exact: true }).click();
  await expect(page.getByRole('dialog', { name: 'published-review' })).toBeVisible();
  await page.getByRole('tab', { name: 'Overview', exact: true }).click();
  await expect(page.getByRole('dialog', { name: 'published-review' })).toContainText(
    'https://github.com/ui2-fixture/published',
  );
  const publishedList = await (
    await page.request.get('/api/skills', { headers: { 'X-Workspace-ID': ws } })
  ).json();
  const published = publishedList.find((s: SkillSummary) => s.name === 'published-review');
  const headers = { 'X-Workspace-ID': ws };
  expect(
    await (
      await page.request.get(`/api/skills/${published.id}/files?path=references%2Fraw.bin`, {
        headers,
      })
    ).body(),
  ).toEqual(Buffer.from([0, 255, 128, 13, 10]));
  expect(
    await (
      await page.request.get(`/api/skills/${published.id}/files?path=scripts%2Fcheck.py`, {
        headers,
      })
    ).text(),
  ).toBe("print('never executed')\n");
  expect(
    await (
      await page.request.get(`/api/skills/${published.id}/files?path=SKILL.md`, { headers })
    ).text(),
  ).toBe(
    '---\nname: published-review\ndescription: Published complete folder.\n---\n\n# Published review\n',
  );
});

test('runtime import shows per-item results', async ({ page }) => {
  await skill(page, 'runtime-duplicate');
  await open(page);
  await page.getByRole('button', { name: 'Add skill', exact: true }).click();
  await page.getByRole('button', { name: 'Copy from runtime', exact: false }).click();
  const picker = page.getByRole('combobox', { name: 'Runtime', exact: true });
  await expect(picker.locator('option')).not.toHaveCount(1);
  await picker.selectOption({ label: 'lab-mac · Claude Code' });
  await page.getByRole('checkbox', { name: 'runtime-review', exact: false }).check();
  await page.getByRole('checkbox', { name: 'runtime-duplicate', exact: false }).check();
  await page.getByRole('button', { name: 'Import to workspace', exact: true }).click();
  await expect(page.getByText('runtime-review · Imported', { exact: true })).toBeVisible();
  await expect(page.getByText(/runtime-duplicate · conflict/)).toBeVisible();
  await page.screenshot({ path: 'test-results/skills/runtime-partial.png' });
  await page.getByRole('button', { name: 'Done', exact: true }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await page.getByRole('searchbox', { name: 'Search skills' }).fill('runtime-review');
  await page.getByRole('button', { name: 'runtime-review', exact: true }).click();
  await expect(page.getByRole('dialog', { name: 'runtime-review' })).toBeVisible();
});

test('bundle is read-only and colleague cannot edit my import', async ({ page, browser }) => {
  const { ws, headers, detail } = await skill(page, `owned-${Date.now()}`);
  await open(page);
  await page.getByRole('searchbox', { name: 'Search skills' }).fill('aeon');
  await page.getByRole('button', { name: 'aeon', exact: true }).click();
  await expect(page.getByRole('dialog')).toContainText('K-Dense');
  await page.getByRole('tab', { name: 'Files', exact: true }).click();
  await expect(page.getByRole('dialog').locator('.file-preview')).toContainText('aeon');
  await expect(page.getByRole('textbox', { name: 'SKILL.md', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Edit', exact: true })).toHaveCount(0);
  await page.keyboard.press('Escape');
  const email = `skills-${Date.now()}@lab.test`;
  const invite = await (
    await page.request.post(`/api/workspaces/${ws}/invitations`, {
      headers,
      data: { email, role: 'member' },
    })
  ).json();
  const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  try {
    await labSignIn(context.request, email);
    expect((await context.request.patch('/api/me', { data: { onboarded: true } })).status()).toBe(
      200,
    );
    expect((await context.request.post(`/api/invitations/${invite.token}/accept`)).status()).toBe(
      200,
    );
    const colleague = await context.newPage();
    await colleague.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), ws);
    await open(colleague);
    await colleague.getByRole('searchbox', { name: 'Search skills' }).fill(detail.name);
    await colleague.getByRole('button', { name: detail.name, exact: true }).click();
    await expect(colleague.getByRole('button', { name: 'Edit', exact: true })).toHaveCount(0);
    expect(
      (
        await context.request.patch(`/api/skills/${detail.id}`, {
          headers,
          data: { revision: 1, content: 'forbidden' },
        })
      ).status(),
    ).toBe(403);
  } finally {
    await context.close();
  }
});

test('stale edit is rejected without losing the draft', async ({ page }) => {
  const { headers, detail } = await skill(page, `stale-${Date.now()}`);
  await open(page);
  await page.getByRole('searchbox', { name: 'Search skills' }).fill(detail.name);
  await page.getByRole('button', { name: detail.name, exact: true }).click();
  await page.getByRole('button', { name: 'Edit', exact: true }).click();
  await page.getByRole('textbox', { name: 'SKILL.md', exact: true }).fill('my unsaved text');
  expect(
    (
      await page.request.patch(`/api/skills/${detail.id}`, {
        headers,
        data: { revision: 1, content: text(detail.name) + 'remote change' },
      })
    ).status(),
  ).toBe(200);
  await expect(page.getByText(/saved version has changed/i)).toBeVisible();
  await expect(page.getByRole('textbox', { name: 'SKILL.md', exact: true })).toHaveValue(
    'my unsaved text',
  );
  await expect(page.getByRole('button', { name: 'Save changes', exact: true })).toBeDisabled();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog', { name: 'Discard unsaved changes?' })).toBeVisible();
  await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(page.getByRole('textbox', { name: 'SKILL.md', exact: true })).toHaveValue(
    'my unsaved text',
  );
});

test('skill modal follows all four palettes and keyboard focus', async ({ page }) => {
  const { detail } = await skill(page, `palette-${Date.now()}`, true);
  for (const theme of ['ash', 'midnight', 'dawn', 'pale']) {
    await page.goto('/#/settings/preferences');
    await page
      .getByRole('button', { name: theme[0]!.toUpperCase() + theme.slice(1), exact: true })
      .click();
    await open(page);
    await page.getByRole('searchbox', { name: 'Search skills' }).fill(detail.name);
    await page.getByRole('button', { name: detail.name, exact: true }).click();
    await expect(
      page.getByRole('dialog', { name: detail.name }).getByText('You', { exact: true }),
    ).toBeVisible();
    await page.getByRole('button', { name: 'Edit', exact: true }).focus();
    await page.keyboard.press('Tab');
    await expect(page.getByRole('button', { name: 'Delete skill', exact: true })).toBeFocused();
    await page.screenshot({ path: `test-results/skills/${theme}-modal.png` });
    await expect(page.getByRole('dialog', { name: detail.name })).toBeVisible();
    await page.keyboard.press('Escape');
  }
});

async function archive(name: string) {
  const { labSkillArchive } = await import('./lab');
  return labSkillArchive(name);
}

test('a real 409 save preserves its draft and requires explicit reload', async ({ page }) => {
  const { headers, detail } = await skill(page, `cas-${Date.now()}`);
  await open(page);
  await page.getByRole('searchbox', { name: 'Search skills' }).fill(detail.name);
  await page.getByRole('button', { name: detail.name, exact: true }).click();
  await page.getByRole('button', { name: 'Edit', exact: true }).click();
  const draft = text(detail.name) + 'my unsaved text';
  await page.getByRole('textbox', { name: 'SKILL.md', exact: true }).fill(draft);
  await page.route(`**/api/skills/${detail.id}`, async (route) => {
    if (route.request().method() === 'PATCH') {
      expect(
        (
          await page.request.patch(`/api/skills/${detail.id}`, {
            headers,
            data: { revision: 1, content: text(detail.name) + 'real concurrent change' },
          })
        ).status(),
      ).toBe(200);
    }
    await route.continue();
  });
  const refusal = page.waitForResponse(
    (r) =>
      r.url().endsWith(`/api/skills/${detail.id}`) &&
      r.request().method() === 'PATCH' &&
      r.status() === 409,
  );
  await page.getByRole('button', { name: 'Save changes', exact: true }).click();
  await refusal;
  await expect(page.getByRole('textbox', { name: 'SKILL.md', exact: true })).toHaveValue(draft);
  await expect(page.getByRole('button', { name: 'Save changes', exact: true })).toBeDisabled();
  await page.getByRole('button', { name: 'Reload saved version', exact: true }).click();
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
  await expect(page.getByRole('textbox', { name: 'SKILL.md', exact: true })).toHaveValue(
    text(detail.name) + 'real concurrent change',
  );
});

test('new skill forms guard navigation and preserve text and selected files', async ({ page }) => {
  await open(page);
  await page.getByRole('button', { name: 'Add skill', exact: true }).click();
  await page.getByRole('button', { name: 'Create manually', exact: false }).click();
  await page.getByRole('textbox', { name: 'Name', exact: true }).fill('unsaved-new-skill');
  await page.evaluate(() => {
    location.hash = '/studies';
  });
  await expect(page.getByRole('dialog', { name: 'Discard unsaved changes?' })).toBeVisible();
  await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(page).toHaveURL(/settings\/skills/);
  await expect(page.getByRole('textbox', { name: 'Name', exact: true })).toHaveValue(
    'unsaved-new-skill',
  );
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
  await page.getByRole('button', { name: 'Add skill', exact: true }).click();
  await page.getByRole('button', { name: 'Import from local', exact: false }).click();
  await page.locator('input[data-archive]').setInputFiles({
    name: 'selected.zip',
    mimeType: 'application/zip',
    buffer: await archive('selected'),
  });
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog', { name: 'Discard unsaved changes?' })).toBeVisible();
  await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(page.getByText('selected.zip', { exact: true })).toBeVisible();
});

test('folder upload preserves relative paths and hidden supporting bytes', async ({ page }) => {
  const name = `folder-${Date.now()}`,
    dir = labSkillFolder(name);
  try {
    await open(page);
    await page.getByRole('button', { name: 'Add skill', exact: true }).click();
    await page.getByRole('button', { name: 'Import from local', exact: false }).click();
    await page.locator('input[data-folder]').setInputFiles(dir);
    await expect(page.getByRole('dialog', { name: 'Import from local' })).toContainText(
      'Contains scripts',
    );
    await page.getByRole('button', { name: 'Import', exact: true }).click();
    await expect(page.getByRole('dialog', { name })).toBeVisible();
    const ws = await labWorkspace(page.request),
      headers = { 'X-Workspace-ID': ws };
    const list = await (await page.request.get('/api/skills', { headers })).json(),
      id = list.find((s: SkillSummary) => s.name === name).id;
    const detail = await (await page.request.get(`/api/skills/${id}`, { headers })).json();
    expect(detail.files.map((f: SkillFileMeta) => f.path)).toEqual([
      '.hidden',
      'SKILL.md',
      'references/raw.bin',
      'scripts/check.py',
    ]);
    expect(
      await (
        await page.request.get(`/api/skills/${id}/files?path=references%2Fraw.bin`, { headers })
      ).body(),
    ).toEqual(Buffer.from([0, 255, 128, 13, 10]));
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('a late URL import completes only in its captured workspace', async ({ page }) => {
  const source = await labWorkspace(page.request);
  const newWorkspace = await (
    await page.request.post('/api/workspaces', {
      data: { name: 'Late destination', slug: `late-${Date.now()}` },
    })
  ).json();
  await open(page);
  await page.getByRole('button', { name: 'Add skill', exact: true }).click();
  await page.getByRole('button', { name: 'Import from URL', exact: false }).click();
  await page
    .getByRole('textbox', { name: 'Skill URL' })
    .fill('https://github.com/ui2-fixture/late');
  await page.getByRole('button', { name: 'Import', exact: true }).click();
  await expect
    .poll(async () => (await (await page.request.get(`${LAB}/lab/skill-source`)).json()).started)
    .toBe(true);
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
  await page.locator('.workspace-switch').click();
  await page.getByRole('menuitem', { name: 'Late destination', exact: false }).click();
  await expect(page.locator('.workspace-switch')).toContainText('Late destination');
  await open(page);
  expect((await page.request.post(`${LAB}/lab/skill-source/release`)).status()).toBe(204);
  await expect
    .poll(async () => {
      const list = await (
        await page.request.get('/api/skills', { headers: { 'X-Workspace-ID': source } })
      ).json();
      return list.some((s: SkillSummary) => s.name === 'late-review');
    })
    .toBe(true);
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await page.getByRole('searchbox', { name: 'Search skills' }).fill('late-review');
  await expect(page.getByText('No skills match your search.', { exact: true })).toBeVisible();
  const list = await (
    await page.request.get('/api/skills', { headers: { 'X-Workspace-ID': newWorkspace.id } })
  ).json();
  expect(list.some((s: SkillSummary) => s.name === 'late-review')).toBe(false);
});

test('admin permission loss retains the editor draft and disables writes', async ({
  page,
  browser,
}) => {
  const { ws, headers, detail } = await skill(page, `permissions-${Date.now()}`);
  const email = `admin-${Date.now()}@lab.test`;
  const invitation = await (
    await page.request.post(`/api/workspaces/${ws}/invitations`, {
      headers,
      data: { email, role: 'admin' },
    })
  ).json();
  const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  try {
    await labSignIn(context.request, email);
    expect((await context.request.patch('/api/me', { data: { onboarded: true } })).status()).toBe(
      200,
    );
    expect(
      (await context.request.post(`/api/invitations/${invitation.token}/accept`)).status(),
    ).toBe(200);
    const me = await (await context.request.get('/api/me')).json();
    const editor = await context.newPage();
    await editor.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), ws);
    await open(editor);
    await editor.getByRole('searchbox', { name: 'Search skills' }).fill(detail.name);
    await editor.getByRole('button', { name: detail.name, exact: true }).click();
    await editor.getByRole('button', { name: 'Edit', exact: true }).click();
    const draft = text(detail.name) + 'admin unsaved text';
    await editor.getByRole('textbox', { name: 'SKILL.md', exact: true }).fill(draft);
    await editor.route(`**/api/skills/${detail.id}`, async (route) => {
      if (route.request().method() === 'PATCH') {
        expect(
          (
            await page.request.patch(`/api/workspaces/${ws}/members/${me.id}`, {
              headers,
              data: { role: 'member' },
            })
          ).status(),
        ).toBe(204);
      }
      await route.continue();
    });
    const refusal = editor.waitForResponse(
      (r) =>
        r.url().endsWith(`/api/skills/${detail.id}`) &&
        r.request().method() === 'PATCH' &&
        r.status() === 403,
    );
    await editor.getByRole('button', { name: 'Save changes', exact: true }).click();
    await refusal;
    await expect(
      editor.getByText('This skill is no longer editable. Your draft is preserved.', {
        exact: true,
      }),
    ).toBeVisible();
    await expect(editor.getByRole('textbox', { name: 'SKILL.md', exact: true })).toHaveValue(draft);
    await expect(editor.getByRole('button', { name: 'Save changes', exact: true })).toBeDisabled();
    expect(
      (
        await page.request.patch(`/api/workspaces/${ws}/members/${me.id}`, {
          headers,
          data: { role: 'admin' },
        })
      ).status(),
    ).toBe(204);
    await expect(editor.getByRole('button', { name: 'Save changes', exact: true })).toBeEnabled();
    await expect(editor.getByRole('textbox', { name: 'SKILL.md', exact: true })).toHaveValue(draft);
  } finally {
    await context.close();
  }
});

test('single runtime copy opens its editor only after Done', async ({ page }) => {
  await open(page);
  await page.getByRole('button', { name: 'Add skill', exact: true }).click();
  await page.getByRole('button', { name: 'Copy from runtime', exact: false }).click();
  await page
    .getByRole('combobox', { name: 'Runtime', exact: true })
    .selectOption({ label: 'lab-mac · Claude Code' });
  await page.getByRole('checkbox', { name: 'runtime-single', exact: false }).check();
  await page.getByRole('button', { name: 'Import to workspace', exact: true }).click();
  await expect(page.getByText('runtime-single · Imported', { exact: true })).toBeVisible();
  await expect(page.getByRole('dialog', { name: 'runtime-single', exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: 'Done', exact: true }).click();
  await expect(
    page
      .getByRole('dialog', { name: 'runtime-single', exact: true })
      .getByRole('textbox', { name: 'SKILL.md', exact: true }),
  ).toBeVisible();
});

test('duplicate names and unsupported URLs keep the entered form and real error', async ({
  page,
}) => {
  await open(page);
  await page.getByRole('button', { name: 'Add skill', exact: true }).click();
  await page.getByRole('button', { name: 'Create manually', exact: false }).click();
  await page.getByRole('textbox', { name: 'Name', exact: true }).fill('aeon');
  await page.getByRole('button', { name: 'Create skill', exact: true }).click();
  await expect(
    page.getByRole('dialog', { name: 'Create manually' }).getByRole('alert'),
  ).toContainText('rename');
  await expect(page.getByRole('textbox', { name: 'Name', exact: true })).toHaveValue('aeon');
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
  await page.getByRole('button', { name: 'Add skill', exact: true }).click();
  await page.getByRole('button', { name: 'Import from URL', exact: false }).click();
  await page
    .getByRole('textbox', { name: 'Skill URL', exact: true })
    .fill('https://unsupported.example/skill');
  await page.getByRole('button', { name: 'Import', exact: true }).click();
  await expect(
    page.getByRole('dialog', { name: 'Import from URL' }).getByRole('alert'),
  ).toContainText(/unsupported/i);
  await expect(page.getByRole('textbox', { name: 'Skill URL', exact: true })).toHaveValue(
    'https://unsupported.example/skill',
  );
});

async function selectedRuntimeSkill(page: Page) {
  await open(page);
  await page.getByRole('button', { name: 'Add skill', exact: true }).click();
  await page.getByRole('button', { name: 'Copy from runtime', exact: false }).click();
  const picker = page.getByRole('combobox', { name: 'Runtime', exact: true });
  await expect(picker.locator('option')).not.toHaveCount(1);
  await picker.selectOption({ label: 'lab-mac · Claude Code' });
  const checkbox = page.getByRole('checkbox', { name: 'runtime-review', exact: false });
  await checkbox.check();
  return { picker, checkbox, runtime: await picker.inputValue() };
}

test('runtime Back preserves selected skills until explicit discard', async ({ page }) => {
  const { picker, checkbox, runtime } = await selectedRuntimeSkill(page);
  await page.getByRole('button', { name: 'Back to methods', exact: true }).click();
  await expect(page.getByRole('dialog', { name: 'Discard unsaved changes?' })).toBeVisible();
  await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(picker).toHaveValue(runtime);
  await expect(checkbox).toBeChecked();
  await page.getByRole('button', { name: 'Back to methods', exact: true }).click();
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
  await expect(page.getByRole('dialog', { name: 'Add skill', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Copy from runtime', exact: false }).click();
  await expect(picker).toHaveValue('');
  await picker.selectOption({ label: 'lab-mac · Claude Code' });
  await expect(checkbox).not.toBeChecked();
});

test('runtime change preserves identity and selected skills until explicit discard', async ({
  page,
}) => {
  const { picker, checkbox, runtime } = await selectedRuntimeSkill(page);
  await picker.selectOption('');
  await expect(page.getByRole('dialog', { name: 'Discard unsaved changes?' })).toBeVisible();
  await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(picker).toHaveValue(runtime);
  await expect(checkbox).toBeChecked();
  await picker.selectOption('');
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
  await expect(picker).toHaveValue('');
  await expect(checkbox).toHaveCount(0);
  await picker.selectOption({ label: 'lab-mac · Claude Code' });
  await expect(checkbox).not.toBeChecked();
});

test('skill module-load failure keeps complete safe recorded instructions available', async ({
  page,
}) => {
  const name = `renderer-fallback-${Date.now()}`;
  const s = await skill(page, name);
  const instructions =
    '# Complete recorded method\n\n' +
    'Evidence remains available.\n'.repeat(150) +
    '<script>window.skillExecuted=true</script>\n![inert](https://example.com/tracker.png)\nend-of-skill-fallback';
  const content = `---\nname: ${name}\ndescription: Review a scientific result.\n---\n\n${instructions}`;
  expect(
    (
      await page.request.patch(`/api/skills/${s.detail.id}`, {
        headers: s.headers,
        data: { revision: s.detail.revision, content },
      })
    ).status(),
  ).toBe(200);
  await page.route('**/RecordedMarkdown.vue*', (route) => route.abort('failed'));
  await open(page);
  await page.getByRole('button', { name, exact: true }).click();
  const dialog = page.getByRole('dialog', { name, exact: true });
  await expect(dialog.getByRole('status')).toHaveText(
    'Markdown formatting is unavailable; showing recorded text.',
  );
  await expect(dialog.locator('.recorded-text-fallback')).toHaveText(instructions);
  await expect(dialog.locator('script,img')).toHaveCount(0);
  expect(await page.evaluate(() => 'skillExecuted' in window)).toBe(false);
  await dialog.getByRole('tab', { name: 'Files', exact: true }).click();
  await expect(dialog.locator('.file-preview')).toHaveText(content);
});
