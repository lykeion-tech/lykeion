import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { labFlow, labInbox, labMember, labWorkspace, newStudy, type FlowRecord } from './lab';

const shots = 'test-results/ui-lab/screenshots';
const H1 = 'Glucose raises insulin secretion in islets';
const H2 = 'Fasting lowers insulin secretion in islets';
const PAPER = 'PMID:31415926';
const SUMMARY = `The study found that glucose raises insulin secretion.\n\n${'Each run kept its recorded values and saved figure. '.repeat(40)}\n\nend-of-hand-in`;

// Finishes a Grounding-to-Validation study through the lab, one column at a
// time, with the records each stage writes. The lab does not run the server's
// finish path, so the owner's report item is seeded the way it raises it.
async function finishedStudy(
  request: APIRequestContext,
  opts: { summary?: boolean; workflow?: string; raised?: boolean } = {},
) {
  const title = `Report study ${Date.now()}`;
  const study = await newStudy(request, 'Does glucose raise insulin secretion?', {
    assignee: null,
    title,
  });
  const step = (column: string, records: FlowRecord[], extra: Record<string, unknown> = {}) =>
    labFlow(request, study.id, {
      column,
      state: 'done',
      scope_from: 'grounding',
      records,
      ...extra,
    });
  await step('grounding', [
    { kind: 'paper', key: 'r1', body: { id: PAPER, text: 'Glucose-stimulated insulin release.' } },
  ]);
  await step('hypotheses', [
    { kind: 'hypothesis', key: 'r2', body: { text: H1, cites: ['r1'] } },
    { kind: 'hypothesis', key: 'r3', body: { text: H2, cites: ['r1'] } },
  ]);
  await step('experimentation', [
    { kind: 'output', key: 'r4', body: { name: 'fig1.png', size_bytes: 1200 } },
    { kind: 'value', key: 'r5', body: { name: 'insulin_ratio', value: '1.4', file: 'fig1.png' } },
  ]);
  const validation: FlowRecord[] = [
    {
      kind: 'validation_status',
      key: 'r6',
      body: { hypothesis: H1, status: 'supported', reasons: [], cites: ['r5'], final: true },
    },
    {
      kind: 'validation_status',
      key: 'r7',
      body: {
        hypothesis: H2,
        status: 'caveated',
        reasons: ['Small sample'],
        cites: [],
        final: true,
      },
    },
    {
      kind: 'objection',
      key: 'r8',
      body: {
        target_key: 'r3',
        label: 'H2',
        type: 'unmatched_control',
        problem: 'The fasting control was not matched by age.',
        server: 'ok',
        checks: [],
        upheld: false,
      },
    },
    {
      kind: 'review',
      key: 'rv1',
      body: {
        column: 'validation',
        attempt: 1,
        decision: 'pass',
        items: [
          {
            key: 'r3',
            kind: 'hypothesis',
            label: H2,
            status: 'pass',
            checks: [
              {
                id: 'criterion:1',
                by: 'jev',
                question: 'Does the recorded ratio support the claim?',
                p: 0.82,
                status: 'pass',
              },
            ],
          },
        ],
      },
    },
    { kind: 'outcome', key: 'o1', body: { verdict: 'high', attempt: 1 } },
  ];
  if (opts.summary !== false)
    validation.push({ kind: 'summary', key: 's1', body: { text: SUMMARY }, run: true });
  await step('validation', validation, opts.workflow ? { workflow: opts.workflow } : {});
  if (opts.raised !== false)
    await labInbox(
      request,
      study.id,
      opts.workflow ? 'done' : 'study_report',
      `Study #${study.number}'s report is ready.`,
    );
  return { ...study, title };
}

async function itemFor(request: APIRequestContext, cardId: string) {
  const ws = await labWorkspace(request);
  const items = (await (
    await request.get('/api/decisions', { headers: { 'X-Workspace-ID': ws } })
  ).json()) as { id: string; card_id: string; kind: string }[];
  return items.find((i) => i.card_id === cardId)!;
}

const graph = (page: Page) => page.locator('.report-graph');
const pane = (page: Page) => page.getByRole('region', { name: 'Report section' });
// The right pane starts closed; its toggle opens it onto the report's sections.
const openPane = async (page: Page) => {
  await page.getByRole('button', { name: 'Toggle report pane' }).click();
  await expect(pane(page)).toBeVisible();
};

test("a finished study's report opens from Decisions with its hand-in, graph and pane", async ({
  page,
}) => {
  const study = await finishedStudy(page.request);
  const item = await itemFor(page.request, study.id);
  expect(item.kind).toBe('study_report');
  await page.goto('/#/decisions');
  await page
    .getByRole('navigation', { name: 'Decisions' })
    .locator(`a[href="#/decisions/${item.id}"]`)
    .click();
  await expect(
    page.getByRole('heading', { name: `#${study.number} ${study.title}` }),
  ).toBeVisible();
  const handIn = page.getByRole('region', { name: 'Hand-in report' });
  await expect(handIn).toContainText('The study found that glucose raises insulin secretion.');
  await expect(handIn).toContainText('end-of-hand-in');
  const body = handIn.locator('.hand-in-text');
  expect(await body.evaluate((e) => e.clientHeight)).toBeLessThanOrEqual(260);
  await handIn.getByRole('button', { name: 'Show the full hand-in' }).click();
  expect(await body.evaluate((e) => e.clientHeight)).toBeGreaterThan(260);
  await handIn.getByRole('button', { name: 'Show less' }).click();

  const report = await (
    await page.request.get(`/api/cards/${study.id}/report`, {
      headers: { 'X-Workspace-ID': await labWorkspace(page.request) },
    })
  ).json();
  // The hand-in says when it was written, under the report's title.
  expect(report.hand_in.run_id).toMatch(/^[0-9a-f-]{36}$/);
  const by = page.locator('.report-heading .hand-in-by');
  const when = await page.evaluate((at) => new Date(at).toLocaleString(), report.hand_in.at);
  await expect(by).toHaveText(new RegExp(when.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
  await expect(handIn.locator('.hand-in-by')).toHaveCount(0);
  await expect(handIn.getByRole('heading', { name: 'Hand-in report' })).toHaveCount(0);
  // The page's own heading is the only one; the shell adds none.
  await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1);
  const nodes = graph(page).getByRole('button');
  await expect(nodes).toHaveCount(report.nodes.length);
  for (const label of [
    `Paper: ${PAPER}`,
    `Hypothesis: ${H1}`,
    `Hypothesis: ${H2}`,
    'Output: fig1.png',
    `Finding: ${H1}`,
    `Finding: ${H2}`,
    'Objection: unmatched control',
  ])
    await expect(graph(page).getByRole('button', { name: label, exact: true })).toBeVisible();
  await expect(page.getByRole('complementary', { name: 'Right pane' })).toHaveCount(0);
  await openPane(page);
  await expect(pane(page).getByRole('heading', { name: 'Overview' })).toBeVisible();
  await expect(pane(page)).toContainText('Findings supported1');
  // The pane is the report and nothing else: no chooser, no tab, no way back to one.
  const right = page.getByRole('complementary', { name: 'Right pane' });
  await expect(right.getByText('Open a surface')).toHaveCount(0);
  await expect(right.getByRole('button', { name: 'Back to surfaces' })).toHaveCount(0);
  await expect(right.getByRole('button', { name: 'Report', exact: true })).toHaveCount(0);
  await expect(right.getByRole('heading', { name: 'Report', exact: true })).toHaveCount(0);
  await expect(right.getByRole('button', { name: 'Maximize pane' })).toBeVisible();
  // One toggle at a time: the page's while the pane floats, the pane's own once it is maximized.
  const pageToggle = page.getByRole('button', { name: 'Toggle report pane' });
  await expect(pageToggle).toBeVisible();
  await expect(right.getByRole('button', { name: 'Close pane' })).toHaveCount(0);
  await right.getByRole('button', { name: 'Maximize pane' }).click();
  await expect(pageToggle).toHaveCount(0);
  await expect(right.getByRole('button', { name: 'Close pane' })).toBeVisible();
  await right.getByRole('button', { name: 'Restore pane size' }).click();
  await expect(pageToggle).toBeVisible();
});

test('clicking a finding opens its section and every row is inspectable', async ({ page }) => {
  const study = await finishedStudy(page.request);
  await page.goto(`/#/decisions/report/${study.id}`);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(
    `#${study.number} ${study.title}`,
  );
  // The open pane floats over the graph, so the node is chosen before it opens.
  await graph(page)
    .getByRole('button', { name: `Finding: ${H2}`, exact: true })
    .click();
  await openPane(page);
  const selected = pane(page).getByRole('region', { name: 'Selected' });
  await expect(selected).toContainText('Supported with caveats');
  await pane(page)
    .getByRole('button', { name: /Checks · evidence/ })
    .click();
  const check = pane(page).getByRole('button', { name: /Criterion 1/ });
  await check.click();
  await expect(check).toHaveAttribute('aria-expanded', 'true');
  await expect(pane(page)).toContainText('Does the recorded ratio support the claim?');
  await page.screenshot({ path: `${shots}/study-report-finding.png` });
  await pane(page)
    .getByRole('button', { name: /^Sources/ })
    .click();
  await pane(page).getByRole('button', { name: PAPER, exact: true }).click();
  await expect(selected).toContainText(PAPER);
  await expect(selected).toContainText('Paper');
  // A move started in the pane takes focus to the new section.
  await expect(selected).toBeFocused();
  await page.screenshot({ path: `${shots}/study-report-source.png`, fullPage: true });
  await pane(page)
    .getByRole('button', { name: `← Back to ${H2}` })
    .click();
  await expect(selected).toContainText(H2);
  await expect(selected).toContainText('Supported with caveats');
});

test('nodes work from the keyboard', async ({ page }) => {
  const study = await finishedStudy(page.request);
  await page.goto(`/#/decisions/report/${study.id}`);
  await openPane(page);
  await page.getByRole('button', { name: 'Show the full hand-in' }).focus();
  await page.keyboard.press('Tab');
  const question = graph(page).getByRole('button', { name: /^Question: / });
  await expect(question).toBeFocused();
  await page.keyboard.press('Enter');
  const selected = pane(page).getByRole('region', { name: 'Selected' });
  await expect(selected).toContainText('Question');
  const live = pane(page).getByRole('status');
  await expect(live).toHaveText(/^Question: Does glucose raise insulin secretion\?$/);
  // Moving between nodes keeps focus in the graph.
  await expect(question).toBeFocused();
  await page.keyboard.press('Tab');
  const paper = graph(page).getByRole('button', { name: `Paper: ${PAPER}`, exact: true });
  await expect(paper).toBeFocused();
  await page.keyboard.press(' ');
  await expect(selected).toContainText(PAPER);
  await expect(live).toHaveText(`Paper: ${PAPER}`);
  await expect(paper).toBeFocused();
});

test('Go to the report on the study page opens the report and Aristotle opens by itself', async ({
  page,
}) => {
  const study = await finishedStudy(page.request);
  await page.goto(`/#/studies/${study.id}`);
  await page.getByRole('link', { name: 'Go to the report' }).click();
  await expect(page).toHaveURL(new RegExp(`#/decisions/report/${study.id}$`));
  await expect(page.getByRole('region', { name: 'Hand-in report' })).toBeVisible();
  const window = page.locator('section.lab-window');
  await expect(window).toBeVisible();
  await expect(window.locator('.chip')).toHaveText(`This study: ${study.title}`);
  await expect(page.getByRole('button', { name: 'Ask Aristotle', exact: true })).toHaveCount(0);
  // Closing it holds for the rest of the visit.
  await window.getByRole('button', { name: 'Minimize' }).click();
  await expect(page.getByRole('button', { name: 'Ask Aristotle', exact: true })).toBeVisible();
  await graph(page)
    .getByRole('button', { name: `Finding: ${H1}`, exact: true })
    .click();
  await expect(page.getByRole('button', { name: 'Ask Aristotle', exact: true })).toBeVisible();
});

test('report page opens Aristotle with the report as context', async ({ page }) => {
  const study = await finishedStudy(page.request);
  await page.goto(`/#/decisions/report/${study.id}`);
  const window = page.locator('section.lab-window');
  await expect(window).toBeVisible();
  await expect(window.locator('.chip')).toHaveText(`This study: ${study.title}`);
  const asked = page.waitForResponse(
    (r) => /\/api\/lab\/chats\/[^/]+\/questions$/.test(r.url()) && r.request().method() === 'POST',
  );
  await window.getByRole('textbox', { name: 'Question' }).fill('What did this study find?');
  await window.getByRole('textbox', { name: 'Question' }).press('Enter');
  const response = await asked;
  expect(response.request().postDataJSON().context_ref).toBe(`report:${study.id}`);
  // The server accepts the report as context; with no machine online it
  // answers that the machine is offline rather than rejecting the context.
  expect([202, 409]).toContain(response.status());
});

test('a non-participant opens the report by URL and has no Decisions item', async ({
  page,
  browser,
}) => {
  const study = await finishedStudy(page.request);
  const ws = await labWorkspace(page.request);
  const other = await labMember(browser, page.request, ws);
  try {
    await other.page.goto(`/#/decisions/report/${study.id}`);
    await expect(
      other.page.getByRole('heading', { name: `#${study.number} ${study.title}` }),
    ).toBeVisible();
    await expect(other.page.getByRole('region', { name: 'Hand-in report' })).toContainText(
      'end-of-hand-in',
    );
    const items = (await (
      await other.context.request.get('/api/decisions', { headers: { 'X-Workspace-ID': ws } })
    ).json()) as { card_id: string }[];
    expect(items.filter((i) => i.card_id === study.id)).toHaveLength(0);
  } finally {
    await other.context.close();
  }
});

test('report page without a hand-in', async ({ page }) => {
  const study = await finishedStudy(page.request, { summary: false });
  await page.goto(`/#/decisions/report/${study.id}`);
  await expect(page.getByRole('region', { name: 'Hand-in report' })).toContainText(
    'The agents left no hand-in report.',
  );
  await expect(
    graph(page).getByRole('button', { name: `Finding: ${H1}`, exact: true }),
  ).toBeVisible();
});

test('a Workflow study has a Done card and no report link', async ({ page }) => {
  const study = await finishedStudy(page.request, { workflow: 'demo-workflow' });
  const item = await itemFor(page.request, study.id);
  expect(item.kind).toBe('done');
  // The Done card opens the study page.
  await page.goto('/#/decisions');
  await page
    .getByRole('navigation', { name: 'Decisions' })
    .locator(`a[href="#/decisions/${item.id}"]`)
    .click();
  await expect(page).toHaveURL(new RegExp(`#/studies/${study.id}$`));
  await expect(page.getByRole('heading', { name: study.title, level: 1 })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Hand-in report' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Go to the report' })).toHaveCount(0);
  await page.goto(`/#/decisions/report/${study.id}`);
  await expect(page.getByText('This study has no report.')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Study report', level: 1 })).toBeAttached();
});

test('a study that finished before reports shipped has no report', async ({ page }) => {
  const study = await finishedStudy(page.request, { raised: false });
  await page.goto(`/#/studies/${study.id}`);
  await expect(page.getByRole('heading', { name: study.title, level: 1 })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Go to the report' })).toHaveCount(0);
  await page.goto(`/#/decisions/report/${study.id}`);
  await expect(page.getByText('This study has no report.')).toBeVisible();
});

test('clicking a hand-in link selects its node and opens its section', async ({ page }) => {
  const study = await finishedStudy(page.request);
  // The report is the server's, with judged concepts and links laid over it.
  // The hand-in is Markdown; a linked phrase may sit inside bold text. Links
  // point only at Grounding sources.
  const text = `Résumé — β-cells 🧪: ${H1}, as **${PAPER}** reports.\n\nInsulin was measured by \`ELISA\`.`;
  let withLinks = false;
  const bytes = (s: string) => new TextEncoder().encode(s).length;
  const at = (s: string) => bytes(text.slice(0, text.indexOf(s)));
  await page.route(`**/api/cards/${study.id}/report`, async (route) => {
    const report = await (await route.fetch()).json();
    const finding = report.nodes.find(
      (n: { kind: string; label: string }) => n.kind === 'finding' && n.label === H1,
    );
    const paper = report.nodes.find((n: { kind: string }) => n.kind === 'paper');
    report.hand_in.text = text;
    report.nodes.push({
      ...finding,
      id: 'concept:insulin',
      kind: 'concept',
      label: 'Insulin',
      status: 'protein',
      checks: [],
      reasons: [],
      quote: '',
      run_id: null,
      facts: [
        {
          subject: 'insulin',
          relation: 'mentioned_in',
          object: finding.id,
          quote: 'Insulin was measured by ELISA.',
          source_node: finding.id,
          p: 0.9,
        },
      ],
    });
    report.edges.push({ from: 'concept:insulin', to: finding.id, kind: 'mentioned_in' });
    report.links = !withLinks
      ? []
      : [{ start: at(PAPER), end: at(PAPER) + bytes(PAPER), node: paper.id, p: 0.9 }];
    report.extras = 'pending';
    report.checks = 'unavailable';
    report.machine = 'lab-mac';
    await route.fulfill({ json: report });
  });
  await page.goto(`/#/decisions/report/${study.id}`);
  const handIn = page.getByRole('region', { name: 'Hand-in report' });
  const body = handIn.locator('.markdown-body');
  await expect(body.locator('strong')).toHaveText(PAPER);
  const plain = await body.evaluate((e) => e.textContent);
  withLinks = true;
  await page.reload();
  await expect(body.locator('strong button.ref')).toHaveText(PAPER);
  // The hand-in still reads as Markdown, and linking changes no text.
  await expect(body.locator('code')).toHaveText('ELISA');
  expect(await body.evaluate((e) => e.textContent)).toBe(plain);
  await expect(page.getByText('Concepts and links are waiting for lab-mac.')).toBeVisible();
  await expect(
    page.getByText("Jev could not judge this report's figure, table and novelty checks."),
  ).toBeVisible();
  await expect(page.getByRole('list', { name: 'Legend' })).toContainText('Concept');
  await expect(graph(page).getByRole('button', { name: 'Concept: Insulin' })).toBeVisible();

  await expect(body.locator('button.ref')).toHaveCount(1);
  const link = handIn.getByRole('button', { name: `${PAPER}, Paper: ${PAPER}` });
  await link.focus();
  await page.keyboard.press('Enter');
  await expect(link).toHaveAttribute('aria-pressed', 'true');
  const selected = pane(page).getByRole('region', { name: 'Selected' });
  await expect(selected).toContainText(PAPER);
  await expect(selected).toContainText('Paper');

  await graph(page).getByRole('button', { name: 'Concept: Insulin' }).click();
  await expect(selected).toContainText('Insulin');
  await expect(selected).toContainText('Protein');
  await pane(page)
    .getByRole('button', { name: /Facts about it/ })
    .click();
  await pane(page)
    .getByRole('button', { name: new RegExp(`Insulin is mentioned in ${H1}`) })
    .click();
  await expect(pane(page)).toContainText('Insulin was measured by ELISA.');
  await expect(pane(page)).toContainText('evidence 90%');
  await page.screenshot({ path: `${shots}/study-report-links.png`, fullPage: true });
});
