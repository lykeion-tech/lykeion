import { expect, test, type Page } from '@playwright/test';
import { labFixture, labFlow, newStudy } from './lab';

const evidence = process.env.LYK_EVIDENCE_DIR;
const longPackage = `very-long-package-name-${'x'.repeat(240)}==1.0`;

// Seeds the fixture study's machine and a run record, and opens its Environment tab.
async function fixtureEnvironment(page: Page) {
  const f = await labFixture(page.request);
  const headers = { 'X-Workspace-ID': f.workspace_id };
  await labFlow(page.request, f.card_id, {
    column: 'grounding',
    state: 'working',
    records: [
      {
        kind: 'run',
        key: 'run-1',
        body: {
          tool: 'run_script',
          language: 'python',
          packages_study: 'numpy==1.26.4\n',
          at: '2026-10-19T09:00:00Z',
        },
      },
      {
        kind: 'run',
        key: 'run-2',
        body: {
          tool: 'run_script',
          language: 'python',
          packages_study: `pandas==2.2.3\n${longPackage}\n`,
          at: '2026-10-19T10:00:00Z',
        },
      },
    ],
  });
  const runtimes = (await (await page.request.get('/api/runtimes', { headers })).json()) as {
    id: string;
    daemon_id: string;
  }[];
  const runtime = runtimes.find((r) => r.daemon_id === 'fixture')!;
  await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), f.workspace_id);
  await page.goto(`/#/studies/${f.card_id}`);
  const study = page.getByRole('region', { name: 'Study' });
  await study.getByRole('tab', { name: 'Environment' }).click();
  return { f, runtime, study };
}

// Reports the machine's bases the way its daemon's heartbeat does.
async function heartbeat(page: Page, ws: string, runtimeId: string, bases: Record<string, string>) {
  const token = await (
    await page.request.post('/api/tokens', { data: { name: 'Environment browser test' } })
  ).json();
  try {
    const res = await page.request.post('/api/daemon/heartbeat', {
      headers: { Authorization: `Bearer ${token.token}` },
      data: { runtimes: [{ workspace_id: ws, runtime_id: runtimeId }], bases },
    });
    expect(res.status()).toBe(200);
  } finally {
    expect((await page.request.delete(`/api/tokens/${token.id}`)).status()).toBe(204);
  }
}

test('the study modal has Overview and Environment tabs moved by arrow keys', async ({ page }) => {
  const study = await newStudy(page.request, 'Which tab opens first?', { assignee: null });
  await page.goto(`/#/studies/${study.id}`);
  const region = page.getByRole('region', { name: 'Study' });
  const overview = region.getByRole('tab', { name: 'Overview' });
  const environment = region.getByRole('tab', { name: 'Environment' });
  await expect(overview).toHaveAttribute('aria-selected', 'true');
  await expect(region.locator('.study-title')).toBeVisible();
  await overview.focus();
  await page.keyboard.press('ArrowRight');
  await expect(environment).toHaveAttribute('aria-selected', 'true');
  await expect(environment).toBeFocused();
  await expect(region.locator('.study-title')).toHaveCount(0);
  await page.keyboard.press('ArrowLeft');
  await expect(overview).toHaveAttribute('aria-selected', 'true');
  await expect(overview).toBeFocused();
  // Another study opens on Overview again.
  await environment.click();
  await expect(environment).toHaveAttribute('aria-selected', 'true');
  const other = await newStudy(page.request, 'Which tab opens next?', { assignee: null });
  await page.goto(`/#/studies/${other.id}`);
  await expect(region.locator('.study-title')).toHaveText('Which tab opens next?');
  await expect(overview).toHaveAttribute('aria-selected', 'true');
  await expect(environment).toHaveAttribute('aria-selected', 'false');
});

test('no machine is claimed missing while the study’s runs are still loading', async ({ page }) => {
  let release!: () => void;
  const held = new Promise<void>((resolve) => (release = resolve));
  await page.route('**/api/cards/*/runs', async (route) => {
    await held;
    await route.continue();
  });
  const { study } = await fixtureEnvironment(page);
  await expect(study.getByRole('heading', { name: 'Machines' })).toBeVisible();
  await expect(study.locator('.env-packages pre')).toContainText('pandas');
  await expect(study.getByText('No machine has run this study yet.')).toHaveCount(0);
  release();
  await expect(
    study.getByRole('listitem').filter({ hasText: 'Recorded transcript' }),
  ).toBeVisible();
  await expect(study.getByText('No machine has run this study yet.')).toHaveCount(0);
});

test('a study with no runs shows both empty states', async ({ page }) => {
  const study = await newStudy(page.request, 'Has anything run yet?', { assignee: null });
  await page.goto(`/#/studies/${study.id}`);
  const region = page.getByRole('region', { name: 'Study' });
  await region.getByRole('tab', { name: 'Environment' }).click();
  await expect(region.getByRole('heading', { name: 'Machines' })).toBeVisible();
  await expect(region.getByText('No machine has run this study yet.')).toBeVisible();
  await expect(region.getByRole('heading', { name: 'Packages added by this study' })).toBeVisible();
  await expect(region.getByText("This study hasn't added any packages.")).toBeVisible();
});

test('Environment lists the machine’s bases live and the latest packages', async ({ page }) => {
  const { f, runtime, study } = await fixtureEnvironment(page);
  const machine = study.getByRole('listitem').filter({ hasText: 'Recorded transcript' });
  const base = (language: string) =>
    machine.locator('.base').filter({ has: page.getByText(language, { exact: true }) });
  await expect(base('Python')).toContainText('Not reported');
  // A heartbeat's runtime.updated refreshes the open tab.
  await heartbeat(page, f.workspace_id, runtime.id, { python: 'ready', r: 'failed: no network' });
  await expect(base('Python')).toContainText('Ready');
  await expect(base('R')).toContainText('Failed');
  await expect(base('R')).toContainText('no network');
  await expect(base('Julia')).toContainText('Not reported');
  const packages = study.locator('.env-packages pre');
  await expect(packages).toHaveCount(1);
  await expect(packages).toContainText('pandas==2.2.3');
  await expect(packages).not.toContainText('numpy');
  expect(await packages.evaluate((el) => getComputedStyle(el).userSelect)).toBe('text');
});

test('at 390 px nothing in Environment is clipped horizontally, in every palette', async ({
  context,
}) => {
  for (const palette of ['ash', 'midnight', 'dawn', 'pale']) {
    // One page per palette, so each load runs only its own init scripts.
    const page = await context.newPage();
    await page.setViewportSize({ width: 390, height: 844 });
    await page.addInitScript(
      (theme) => localStorage.setItem('lykeion.preferences', JSON.stringify({ theme })),
      palette,
    );
    const { study } = await fixtureEnvironment(page);
    await expect(page.locator('html')).toHaveAttribute('data-palette', palette);
    await expect(study.locator('.env-packages pre')).toContainText('pandas');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    for (const el of await study.locator('.environment-tab, .environment-tab *').all()) {
      const box = await el.boundingBox();
      if (box) expect(box.x + box.width).toBeLessThanOrEqual(390.5);
    }
    if (evidence) await page.screenshot({ path: `${evidence}/390-${palette}.png`, fullPage: true });
    await page.close();
  }
});
