import { expect, test } from '@playwright/test';

const origin = process.env.LYK_DOCS_URL || 'http://127.0.0.1:1440';

test('documentation search works with keyboard, escape and empty results', async ({ page }) => {
  await page.goto(`${origin}/docs/`);
  await page.keyboard.press('Control+k');
  const search = page.getByRole('dialog', { name: 'Search documentation' });
  await expect(search).toBeVisible();
  await expect(search.getByRole('searchbox')).toBeFocused();
  await page.keyboard.type('zzzzzz');
  await expect(search.getByRole('status')).toHaveText('No results. Try another search.');
  await page.keyboard.press('Escape');
  await expect(search).not.toBeVisible();
  await expect(page.getByRole('button', { name: /Search documentation/ })).toBeFocused();
  await page.keyboard.press('Enter');
  await search.getByRole('searchbox').fill('Welcome');
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(`${origin}/docs/welcome/`);
  await expect(page.getByRole('navigation', { name: 'Documentation', exact: true }).getByRole('link', { name: 'Welcome to Lykeion' })).toHaveAttribute('aria-current', 'page');
  await page.getByRole('navigation', { name: 'On this page' }).getByRole('link', { name: 'Open your workspace' }).click();
  await expect(page.locator('#open-your-workspace')).toBeInViewport();
});

test('documentation fits narrow screens with reduced motion', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto(`${origin}/docs/welcome/`);
  await expect(page.getByRole('heading', { name: 'Welcome to Lykeion' })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  await page.getByText('Explore documentation', { exact: true }).click();
  await expect(page.getByRole('navigation', { name: 'Documentation', exact: true })).not.toBeVisible();
  await page.getByText('Explore documentation', { exact: true }).click();
  await expect(page.getByRole('navigation', { name: 'Documentation', exact: true })).toBeVisible();
});


test('desktop navigation disclosure remains operable by keyboard and pointer', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(`${origin}/docs/`);
  const disclosure = page.locator('.docs-sidebar summary');
  const navigation = page.getByRole('navigation', { name: 'Documentation', exact: true });
  await disclosure.focus();
  await page.keyboard.press('Space');
  await expect(navigation).not.toBeVisible();
  await disclosure.click();
  await expect(navigation).toBeVisible();
  await disclosure.focus();
  await page.keyboard.press('Enter');
  await expect(navigation).not.toBeVisible();
  await page.keyboard.press('Enter');
  await expect(navigation).toBeVisible();
});

test('article actions copy exact Markdown and links, and report clipboard denial', async ({ page }) => {
  const { getArticle } = await import('../../apps/web/src/app/docs/content');
  const article = getArticle('welcome')!;
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'clipboard', { value: { writeText: async (text: string) => {
      (window as unknown as { copied: string }).copied = text;
    } }, configurable: true });
  });
  await page.goto(`${origin}/docs/welcome/`);
  const actions = page.getByRole('group', { name: 'Article actions' });
  await actions.locator('summary').focus();
  await page.keyboard.press('Enter');
  await page.keyboard.press('Tab');
  await expect(actions.getByRole('button', { name: 'Copy page', exact: true })).toBeFocused();
  expect(await actions.getByRole('button', { name: 'Copy page', exact: true }).evaluate(element => getComputedStyle(element).outlineStyle)).toBe('solid');
  await page.screenshot({ path: '.superpowers/sdd/2026-11-16-lykeion-documentation/task-3-actions-focus.png', fullPage: true });
  await page.keyboard.press('Enter');
  await expect(page.getByRole('status')).toHaveText('Page copied.');
  expect(await page.evaluate(() => (window as unknown as { copied: string }).copied)).toBe(article.markdown);
  await actions.getByRole('button', { name: 'Copy Markdown link' }).click();
  expect(await page.evaluate(() => (window as unknown as { copied: string }).copied)).toBe('https://app.lykeion.tech/docs/welcome/markdown/');
  await expect(actions.getByRole('link', { name: 'View Markdown' })).toHaveAttribute('href', 'https://app.lykeion.tech/docs/welcome/markdown/');
  await expect(actions).not.toContainText(/MCP|Cursor|VS Code/);
  await page.evaluate(() => { Object.defineProperty(navigator, 'clipboard', { value: { writeText: async () => { throw new DOMException('Denied', 'NotAllowedError'); } }, configurable: true }); });
  await actions.getByRole('button', { name: 'Copy page', exact: true }).click();
  await expect(page.getByRole('status')).toHaveText('Could not copy. Open View Markdown and copy the text or URL from your browser.');
  await page.screenshot({ path: '.superpowers/sdd/2026-11-16-lykeion-documentation/task-3-actions-desktop.png', fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  await page.screenshot({ path: '.superpowers/sdd/2026-11-16-lykeion-documentation/task-3-actions-mobile.png', fullPage: true });
});

test('learning diagrams support native keyboard selection, no JavaScript and reduced motion', async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false, reducedMotion: 'reduce' });
  const page = await context.newPage();
  await page.goto(`${origin}/docs/scientific-stages/`);
  const stages = page.getByRole('region', { name: 'Study stage walkthrough' });
  await stages.getByRole('radio', { name: '01 Grounding' }).focus();
  await page.keyboard.press('ArrowRight');
  await expect(stages.getByRole('heading', { name: 'Make alternatives explicit' })).toBeVisible();
  await expect(stages.getByRole('heading', { name: 'Find the foundation' })).not.toBeVisible();
  expect(await stages.locator('.stage-panel:visible').evaluate(element => getComputedStyle(element).animationName)).toBe('none');
  expect(await stages.locator('.stage-panel:visible .scene-gate').evaluate(element => getComputedStyle(element).animationName)).toBe('none');
  expect(await stages.locator('.stage-panel:visible .scene-result').evaluate(element => getComputedStyle(element).animationName)).toBe('none');
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    await page.screenshot({ path: `.superpowers/sdd/2026-11-16-lykeion-documentation/task-5-stages-${width}.png`, fullPage: true });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  }
  await page.goto(`${origin}/docs/core-concepts/`);
  const concepts = page.getByRole('region', { name: 'Core concepts diagram' });
  await concepts.getByRole('radio', { name: '02 Study', exact: true }).focus();
  await page.keyboard.press('ArrowRight');
  await expect(concepts.getByRole('heading', { name: 'Stages', exact: true })).toBeVisible();
  await expect(concepts.getByRole('link', { name: 'Read Scientific stages' })).toHaveAttribute('href', '/docs/scientific-stages/');
  expect(await concepts.locator('.concept-path-2').evaluate(element => getComputedStyle(element).animationName)).toBe('none');
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    await page.screenshot({ path: `.superpowers/sdd/2026-11-16-lykeion-documentation/task-5-concepts-${width}.png`, fullPage: true });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  }
  await context.close();
});

test('learning graphics show each stage and selected relationships with bounded motion', async ({ page }) => {
  await page.goto(`${origin}/docs/scientific-stages/`);
  const stages = page.getByRole('region', { name: 'Study stage walkthrough' });
  for (const [name, heading] of [['01 Grounding', 'Find the foundation'], ['02 Hypotheses', 'Make alternatives explicit'], ['03 Experimentation', 'Put the question to work'], ['04 Validation', 'Challenge the findings']]) {
    await stages.locator(`label[for="learn-stage-${['01 Grounding', '02 Hypotheses', '03 Experimentation', '04 Validation'].indexOf(name)}"]`).click();
    await expect(stages.getByRole('heading', { name: heading })).toBeVisible();
  }
  expect(await stages.locator('.stage-panel:visible').evaluate(element => getComputedStyle(element).animationName)).toBe('learning-reveal');
  expect(await stages.locator('.stage-panel:visible .scene-gate').evaluate(element => getComputedStyle(element).animationName)).toBe('learning-gate');
  expect(await stages.locator('.stage-panel:visible .scene-result').evaluate(element => getComputedStyle(element).animationIterationCount)).toBe('1');
  await stages.screenshot({ animations: 'disabled', path: '.superpowers/sdd/2026-11-16-lykeion-documentation/task-5-validation.png' });
  await page.goto(`${origin}/docs/core-concepts/`);
  const concepts = page.getByRole('region', { name: 'Core concepts diagram' });
  await concepts.locator('label[for="learn-concept-3"]').click();
  await expect(concepts.getByRole('heading', { name: 'Agent', exact: true })).toBeVisible();
  await expect(concepts.getByRole('link', { name: 'Read Agents' })).toBeVisible();
  expect(await concepts.locator('.concept-path-3').evaluate(element => getComputedStyle(element).opacity)).toBe('1');
  expect(await concepts.locator('.concept-path-3').evaluate(element => getComputedStyle(element).animationIterationCount)).toBe('3');
  await concepts.screenshot({ animations: 'disabled', path: '.superpowers/sdd/2026-11-16-lykeion-documentation/task-5-agent.png' });
});

test('reviewed walkthrough explains both validation routes and concept links reverse', async ({ page }) => {
  await page.goto(`${origin}/docs/scientific-stages/`);
  const walkthrough = page.getByRole('region', { name: 'Study stage walkthrough' });
  expect(await page.locator('h1').evaluate(element => element.getBoundingClientRect().top)).toBeLessThan(await walkthrough.evaluate(element => element.getBoundingClientRect().top));
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    if (width === 390) await page.locator('.docs-sidebar summary').click();
    for (let index = 0; index < 4; index++) {
      await walkthrough.locator(`label[for="learn-stage-${index}"]`).click();
      const radio = walkthrough.locator(`#learn-stage-${index}`);
      await expect(radio).toHaveAttribute('aria-controls', `stage-panel-${index}`);
      await expect(walkthrough.locator(`#stage-panel-${index}`)).toBeVisible();
      await expect(walkthrough.locator('.stage-scene:visible')).toContainText(['Source S', 'Evidence from S', 'Compare A and B', 'Claim or mA, mB'][index]);
      if (index === 3) {
        await expect(walkthrough.locator('#stage-panel-3')).toContainText('Without recorded runs');
        await expect(walkthrough.locator('#stage-panel-3')).toContainText('back to Experimentation once');
        await expect(walkthrough.locator('#stage-panel-3')).toContainText('waits for its owner');
      }
      await page.evaluate(() => window.scrollTo(0, 0));
      await page.screenshot({ animations: 'disabled', path: `.superpowers/sdd/2026-11-16-lykeion-documentation/task-5-reviewed-stage-${index}-${width}.png`, fullPage: true });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
    }
  }
  await walkthrough.getByRole('link', { name: 'Read the stage vocabulary below' }).click();
  await expect(page.locator('#read-the-stage-vocabulary')).toBeInViewport();
  await page.goto(`${origin}/docs/core-concepts/`);
  const concepts = page.getByRole('region', { name: 'Core concepts diagram' });
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    if (width === 390) await page.locator('.docs-sidebar summary').click();
    for (const [index, related] of [[0, 'Agent'], [3, 'Workspace'], [1, 'Aristotle'], [10, 'Study']] as const) {
      await concepts.locator(`label[for="learn-concept-${index}"]`).click();
      await expect(concepts.locator(`#concept-panel-${index}`)).toContainText(related);
      await expect(concepts.locator(`#learn-concept-${index}`)).toHaveAttribute('aria-controls', `concept-panel-${index}`);
      await page.evaluate(() => window.scrollTo(0, 0));
      await page.screenshot({ animations: 'disabled', path: `.superpowers/sdd/2026-11-16-lykeion-documentation/task-5-reviewed-concept-${index}-${width}.png`, fullPage: true });
    }
  }
});

test('concept relationship lines avoid unrelated nodes on desktop and phone', async ({ page }) => {
  await page.goto(`${origin}/docs/core-concepts/`);
  const map = page.getByRole('region', { name: 'Core concepts diagram' });
  await map.locator('label[for="learn-concept-0"]').click();
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    if (width === 390) await page.locator('.docs-sidebar summary').click();
    const crossings = await map.evaluate(element => {
      const labels = [...element.querySelectorAll('.concept-choice')].map(label => label.getBoundingClientRect());
      return [...element.querySelectorAll<SVGPathElement>('path')].flatMap(path => {
        const matrix = path.getScreenCTM()!;
        for (let distance = 0; distance <= path.getTotalLength(); distance += 2) {
          const point = path.getPointAtLength(distance).matrixTransform(matrix);
          for (let index = 0; index < labels.length; index++) {
            if (index === Number(path.dataset.source) || index === Number(path.dataset.target)) continue;
            const rect = labels[index];
            if (point.x > rect.left && point.x < rect.right && point.y > rect.top && point.y < rect.bottom) return [`${path.dataset.source}-${path.dataset.target} crosses ${index}`];
          }
        }
        return [];
      });
    });
    expect(crossings).toEqual([]);
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.screenshot({ animations: 'disabled', path: `.superpowers/sdd/2026-11-16-lykeion-documentation/task-5-routing-workspace-${width}.png`, fullPage: true });
  }
});

// Opt in with LYK_DOCS_DEPLOYMENT=1 against isolated Caddy serving apps/web/out.
test.describe('isolated documentation deployment', () => {
  test.skip(process.env.LYK_DOCS_DEPLOYMENT !== '1', 'Requires an isolated Caddy export with no server upstream.');

  test('exported articles and Markdown survive deployment routing', async ({ page, request }) => {
    const { articles } = await import('../../apps/web/src/app/docs/content');
    await page.goto(origin);
    await page.locator('footer').getByRole('link', { name: 'Documentation', exact: true }).click();
    await expect(page).toHaveURL(`${origin}/docs/`);
    for (const article of articles) {
      const response = await page.goto(`${origin}/docs/${article.slug}/`);
      expect(response?.status()).toBe(200);
      await expect(page.getByRole('heading', { name: article.title, exact: true, level: 1 })).toBeVisible();
      const redirect = await request.get(`${origin}/docs/${article.slug}/markdown/`, { maxRedirects: 0 });
      expect(redirect.status()).toBe(308);
      expect(redirect.headers().location).toBe(`/docs/${article.slug}/markdown`);
      const markdown = await request.get(`${origin}/docs/${article.slug}/markdown/`);
      expect(markdown.status()).toBe(200);
      expect(markdown.headers()['content-type']).toMatch(/^text\/markdown(?:;|$)/);
      expect(await markdown.text()).toBe(article.markdown);
      expect(markdown.url()).toBe(`${origin}/docs/${article.slug}/markdown`);
    }
    for (const path of ['/docs/missing-article/', '/docs/welcome/missing/']) {
      expect((await request.get(`${origin}${path}`)).status()).toBe(404);
    }
    const api = await request.get(`${origin}/health`);
    // The isolated Caddy has no server upstream; 502 proves this was proxied.
    expect(api.status()).toBe(502);
    expect(api.headers()['content-type'] || '').not.toContain('text/markdown');
    const appRedirect = await request.get(`${origin}/app`, { maxRedirects: 0 });
    expect(appRedirect.headers().location).toBe('/app/');
    for (const path of ['/privacy/', '/terms/']) {
      const response = await request.get(`${origin}${path}`);
      expect(response.status()).toBe(200);
      expect(response.headers()['content-type']).toContain('text/html');
    }
  });
});
