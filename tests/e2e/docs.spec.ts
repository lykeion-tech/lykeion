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

test('documentation fits narrow screens with a collapsed keyboard and touch menu', async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 390, height: 844 }, hasTouch: true, reducedMotion: 'reduce' });
  const page = await context.newPage();
  await page.goto(`${origin}/docs/welcome/`);
  await expect(page.getByRole('heading', { name: 'Welcome to Lykeion', level: 1 })).toBeInViewport({ ratio: 1 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  const navigation = page.getByRole('navigation', { name: 'Documentation', exact: true });
  const disclosure = page.locator('.docs-sidebar summary');
  await expect(navigation).not.toBeVisible();
  await disclosure.focus();
  await page.keyboard.press('Enter');
  await expect(navigation).toBeVisible();
  await page.keyboard.press('Space');
  await expect(navigation).not.toBeVisible();
  await expect(disclosure).toBeFocused();
  await disclosure.tap();
  await expect(navigation).toBeVisible();
  await disclosure.tap();
  await expect(navigation).not.toBeVisible();
  await context.close();
});

test('desktop navigation is expanded and its links support keyboard focus without JavaScript', async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false, viewport: { width: 1440, height: 1000 } });
  const page = await context.newPage();
  await page.goto(`${origin}/docs/`);
  const navigation = page.getByRole('navigation', { name: 'Documentation', exact: true });
  await expect(navigation).toBeVisible();
  await expect(page.locator('.docs-sidebar summary')).not.toBeVisible();
  const overview = navigation.getByRole('link', { name: 'Overview', exact: true });
  await overview.focus();
  await expect(overview).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(navigation.getByRole('link', { name: 'Welcome to Lykeion', exact: true })).toBeFocused();
  await context.close();
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
  const prompt = 'Read https://app.lykeion.tech/docs/welcome/ so I can ask questions about it.';
  for (const [name, host, path] of [['Open in ChatGPT', 'chatgpt.com', '/'], ['Open in Claude', 'claude.ai', '/new']] as const) {
    const link = actions.getByRole('link', { name });
    const url = new URL((await link.getAttribute('href'))!);
    expect(url.hostname).toBe(host);
    expect(url.pathname).toBe(path);
    expect(url.searchParams.get('q')).toBe(prompt);
    await expect(link).toHaveAttribute('target', '_blank');
    await expect(link).toHaveAttribute('rel', 'noopener noreferrer');
  }
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


test('documentation text meets normal text contrast on its rendered background', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  const measurements: { text: string; ratio: number }[] = [];
  for (const route of ['/docs/', '/docs/welcome/', '/docs/core-concepts/', '/docs/scientific-stages/']) {
    await page.goto(`${origin}${route}`);
    const measure = () => page.locator('.docs-site, .docs-search').evaluateAll(roots => {
      const rgb = (value: string) => value.match(/[\d.]+/g)!.map(Number);
      const luminance = (color: number[]) => color.slice(0, 3).map(value => {
        const channel = value / 255;
        return channel <= .04045 ? channel / 12.92 : ((channel + .055) / 1.055) ** 2.4;
      }).reduce((sum, channel, index) => sum + channel * [.2126, .7152, .0722][index], 0);
      const elements = new Set(roots.flatMap(root => [...root.querySelectorAll<HTMLElement>('*')]));
      return [...elements].filter(element => element.checkVisibility() && [...element.childNodes].some(node => node.nodeType === Node.TEXT_NODE && node.textContent?.trim())).map(element => {
        let background = [255, 255, 255];
        const ancestors = [];
        for (let parent: Element | null = element; parent; parent = parent.parentElement) ancestors.unshift(parent);
        for (const ancestor of ancestors) {
          const color = rgb(getComputedStyle(ancestor).backgroundColor);
          const alpha = color[3] ?? 1;
          background = background.map((channel, index) => color[index] * alpha + channel * (1 - alpha));
          // Use the darkest opaque gradient stop as the contrast bound for light surfaces.
          for (const stop of getComputedStyle(ancestor).backgroundImage.match(/rgba?\([^)]+\)/g) ?? []) {
            const candidate = rgb(stop);
            if ((candidate[3] ?? 1) === 1 && luminance(candidate) < luminance(background)) background = candidate;
          }
        }
        const foreground = rgb(getComputedStyle(element).color);
        const opacity = ancestors.reduce((value, ancestor) => value * Number(getComputedStyle(ancestor).opacity), 1);
        const alpha = (foreground[3] ?? 1) * opacity;
        const textColor = background.map((channel, index) => foreground[index] * alpha + channel * (1 - alpha));
        const light = luminance(background), dark = luminance(textColor);
        return { text: element.textContent!.trim().slice(0, 90), ratio: (Math.max(light, dark) + .05) / (Math.min(light, dark) + .05) };
      });
    });
    measurements.push(...await measure());
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 });
      measurements.push(...await measure());
      await page.screenshot({ path: `.superpowers/sdd/2026-11-16-lykeion-documentation/mobile-contrast-${route.split('/').filter(Boolean)[1] || 'index'}-${width}.png` });
    }
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.getByRole('button', { name: /Search documentation/ }).click();
    measurements.push(...await measure());
  }
  expect(measurements.length).toBeGreaterThan(20);
  expect(measurements.filter(item => item.ratio < 4.5)).toEqual([]);
  console.log(`Rendered text contrast: ${measurements.length} samples, minimum ${Math.min(...measurements.map(item => item.ratio)).toFixed(2)}:1`);
});

test('canonical learning content is copied, exported and searched on desktop and phone', async ({ page, request }) => {
  const { getArticle } = await import('../../apps/web/src/app/docs/content');
  const { stages, concepts, connections } = await import('../../apps/web/src/app/docs/learning-content');
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async (text: string) => { (window as unknown as { copied: string }).copied = text; } } });
  });
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    for (const slug of ['scientific-stages', 'core-concepts']) {
      const article = getArticle(slug)!;
      await page.goto(`${origin}/docs/${slug}/`);
      const actions = page.getByRole('group', { name: 'Article actions' });
      await actions.locator('summary').click();
      await actions.getByRole('button', { name: 'Copy page', exact: true }).click();
      await expect(page.getByRole('status')).toHaveText('Page copied.');
      const copied = await page.evaluate(() => (window as unknown as { copied: string }).copied);
      expect(copied).toBe(article.markdown);
      expect(await (await request.get(`${origin}/docs/${slug}/markdown/`)).text()).toBe(copied);
      if (slug === 'scientific-stages') {
        for (const stage of stages) { expect(copied).toContain(stage.gate); expect(copied).toContain(stage.result); }
        await page.locator('label[for="learn-stage-3"]').click();
        await expect(page.locator('#stage-panel-3')).toContainText(stages[3].result);
        expect((await page.locator('.docs-prose').allTextContents()).join(' ')).not.toContain(stages[3].result);
      } else {
        for (const [index, concept] of concepts.entries()) {
          expect(copied).toContain(concept.text);
          expect(copied).toContain(connections(index).map(target => concepts[target].name).join(' · '));
        }
        await page.locator('label[for="learn-concept-3"]').click();
        await expect(page.locator('#concept-panel-3')).toContainText('Workspace');
      }
      await page.evaluate(() => window.scrollTo(0, 0));
      await page.screenshot({ animations: 'disabled', fullPage: true, path: `.superpowers/sdd/2026-11-16-lykeion-documentation/canonical-${slug}-copy-${width}.png` });
      await page.getByRole('button', { name: /Search documentation/ }).click();
      const search = page.getByRole('dialog', { name: 'Search documentation' });
      await search.getByRole('searchbox').fill(slug === 'scientific-stages' ? 'falsifiability' : 'reusable research role');
      await expect(search.getByRole('link', { name: new RegExp(article.title) })).toBeVisible();
      await page.screenshot({ animations: 'disabled', path: `.superpowers/sdd/2026-11-16-lykeion-documentation/canonical-${slug}-search-${width}.png` });
      await page.keyboard.press('Escape');
      await expect(search).not.toBeVisible();
    }
  }
});


test('initial concept motion stops within five seconds and focus contrasts with its surroundings', async ({ page }) => {
  for (const slug of ['core-concepts', 'scientific-stages']) {
    await page.goto(`${origin}/docs/${slug}/`);
    if (slug === 'core-concepts') {
      const duration = await page.locator('.concept-path').evaluateAll(paths => Math.max(...paths.map(path => {
        const style = getComputedStyle(path);
        return parseFloat(style.animationDuration) * Number(style.animationIterationCount) || 0;
      })));
      expect(duration).toBeGreaterThan(0);
      expect.soft(duration).toBeLessThanOrEqual(5);
    }
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 1000 });
      const targets = [page.getByRole('button', { name: /Search documentation/ }), page.locator('.docs-actions summary'), page.locator('.learning-radio:checked')];
      if (width === 390) targets.push(page.locator('.docs-sidebar summary'));
      for (const target of targets) {
        await target.focus();
        await page.keyboard.press('Tab');
        await page.keyboard.press('Shift+Tab');
        const measurement = await target.evaluate(element => {
          const visible = element.classList.contains('learning-radio') ? element.nextElementSibling! : element;
          const style = getComputedStyle(visible);
          const rgb = (value: string) => value.match(/[\d.]+/g)!.map(Number);
          const luminance = (color: number[]) => color.slice(0, 3).map(value => { const c = value / 255; return c <= .04045 ? c / 12.92 : ((c + .055) / 1.055) ** 2.4; }).reduce((sum, c, i) => sum + c * [.2126, .7152, .0722][i], 0);
          let background = [255, 255, 255];
          const ancestors = [];
          for (let parent = visible.parentElement; parent; parent = parent.parentElement) ancestors.unshift(parent);
          for (const ancestor of ancestors) {
            const style = getComputedStyle(ancestor);
            const color = rgb(style.backgroundColor); const alpha = color[3] ?? 1;
            background = background.map((c, i) => color[i] * alpha + c * (1 - alpha));
            for (const stop of style.backgroundImage.match(/rgba?\([^)]+\)/g) ?? []) {
              const candidate = rgb(stop);
              if ((candidate[3] ?? 1) === 1 && luminance(candidate) < luminance(background)) background = candidate;
            }
          }
          const ring = rgb(style.outlineColor);
          const a = luminance(background), b = luminance(ring);
          return { style: style.outlineStyle, width: parseFloat(style.outlineWidth), ratio: (Math.max(a, b) + .05) / (Math.min(a, b) + .05) };
        });
        expect(measurement.style).toBe('solid');
        expect(measurement.width).toBeGreaterThanOrEqual(2);
        expect(measurement.ratio).toBeGreaterThanOrEqual(3);
        console.log(`Focus ${slug} ${width}: ${JSON.stringify(measurement)}`);
      }
    }
  }
});


test('audited documentation pages fit desktop and phone', async ({ page }) => {
  for (const slug of ['complete-tutorial', 'evidence-and-results', 'core-concepts', 'scientific-stages']) {
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 });
      await page.goto(`${origin}/docs/${slug}/`);
      await expect(page.locator('h1')).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
      await page.screenshot({ animations: 'disabled', fullPage: true, path: `.superpowers/sdd/2026-11-16-lykeion-documentation/audit-${slug}-${width}.png` });
    }
  }
});


test('search close label includes its visible name and Escape shortcut', async ({ page }) => {
  await page.goto(`${origin}/docs/connect-machine/`);
  await page.getByRole('button', { name: /Search documentation/ }).click();
  const close = page.getByRole('button', { name: 'Close search Esc' });
  await expect(close).toContainText('Close search');
  await expect(close).toHaveAttribute('aria-keyshortcuts', 'Escape');
  await page.keyboard.press('Escape');
});

test('article commands support keyboard scrolling without JavaScript', async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false });
  const page = await context.newPage();
  await page.goto(`${origin}/docs/connect-machine/`);
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    const code = page.locator('.docs-prose pre').first();
    await expect(code).toHaveAttribute('tabindex', '0');
    await expect(code).toHaveAttribute('aria-label', 'Code example');
    await code.focus();
    await page.keyboard.press('Tab');
    await page.keyboard.press('Shift+Tab');
    await expect(code).toBeFocused();
    expect(await code.evaluate(element => getComputedStyle(element).outlineStyle)).toBe('solid');
    await page.keyboard.press('ArrowRight');
    await expect.poll(() => code.evaluate(element => element.scrollLeft)).toBeGreaterThan(0);
    await code.scrollIntoViewIfNeeded();
    await page.screenshot({ path: `.superpowers/sdd/2026-11-16-lykeion-documentation/axe-code-focus-${width}.png` });
  }
  await page.goto(`${origin}/docs/scientific-stages/`);
  await expect(page.getByRole('group', { name: 'Illustrative evidence and gate' }).first()).toBeVisible();
  await context.close();
});
