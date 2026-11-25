import { expect, test } from './test';
import { installAristotleFixture } from './aristotle-fixture';

test('an explicit Coresearch request produces a launchable inline study draft', async ({
  page,
}) => {
  await page.goto('/#/coresearch/new');
  await page
    .getByRole('textbox', { name: 'Message the agent' })
    .fill('Draft a study for the street canopy smoke test.');
  await page.getByRole('button', { name: 'Send', exact: true }).click();
  const card = page.getByRole('region', { name: 'Study draft' });
  await expect(card.getByRole('heading', { name: 'Street canopy smoke test' })).toBeVisible();
  await card.getByRole('button', { name: 'Start study' }).click();
  const study = page.getByRole('link', { name: 'Open study' });
  await expect(study).toHaveAttribute('href', /\/studies\/[a-f0-9-]+$/);
  await page.reload();
  await expect(study).toBeVisible();
});

test('Coresearch drafts open the study composer and retain the created study link', async ({
  page,
}) => {
  await installAristotleFixture(page, true);
  const reply = {
    id: 'reply',
    chat_id: 'chat',
    kind: 'reply',
    body: 'Draft protocol text',
    agent_run_id: 'draft-turn',
    status: 'completed',
    study_draft: {
      title: 'Street heat',
      description: 'Compare matched streets.',
      stage: 'grounding',
      prose: 'Would you like to start a study?',
    },
  };
  let studyId: string | undefined;
  await page.route('**/private-project/chats/chat/messages', (route) =>
    route.fulfill({
      json: [
        {
          id: 'turn',
          kind: 'turn',
          agent_run_id: 'draft-turn',
          body: 'Start a study.',
          status: 'completed',
        },
        { ...reply, study_id: studyId },
      ],
    }),
  );
  await page.route('**/api/teams?*', (route) =>
    route.fulfill({
      json: [
        { id: 'grounding-team', name: 'Grounding Team', stage: 'grounding', source: 'shipped' },
        { id: 'validation-team', name: 'Validation Team', stage: 'validation', source: 'shipped' },
      ],
    }),
  );
  await page.route('**/api/researches', (route) =>
    route.fulfill({ json: [{ id: 'heat-project', name: 'Urban Heat' }] }),
  );
  await page.route('**/api/study-proposals/reply/start', (route) => {
    expect(route.request().postDataJSON()).toMatchObject({
      operation_id: 'reply',
      title: 'Street heat',
      description: 'Compare matched streets.',
      stage: 'validation',
      priority: 'high',
      project_id: 'heat-project',
      assignee: { type: 'team', pack_id: 'validation-team' },
      coresearch: { project_id: 'private-project', chat_id: 'chat' },
    });
    studyId = 'started-study';
    return route.fulfill({ json: { card_id: studyId } });
  });
  await page.goto('/#/coresearch/conversations/welcome');
  await expect(page.getByText('Would you like to start a study?')).toBeVisible();
  await expect(page.getByText('Draft protocol text')).toHaveCount(0);
  const card = page.getByRole('region', { name: 'Study draft' });
  await card.getByRole('button', { name: 'Edit draft' }).click();
  const review = page.getByRole('dialog', { name: 'New study' });
  await expect(review.getByRole('textbox', { name: 'Study title' })).toHaveValue('Street heat');
  await review.getByRole('button', { name: 'Study priority' }).click();
  await review.getByRole('menuitemradio', { name: 'High', exact: true }).click();
  await review.getByRole('button', { name: 'Stages: Grounding' }).click();
  await review.getByRole('menuitemcheckbox', { name: 'Validation' }).click();
  await review.getByRole('button', { name: 'No project' }).click();
  await review.getByRole('menuitemradio', { name: 'Urban Heat' }).click();
  await review.getByRole('button', { name: /^Create study/ }).click();
  await expect(review).toBeHidden();
  await expect(page.getByRole('link', { name: 'Open study' })).toHaveAttribute(
    'href',
    /\/studies\/started-study$/,
  );
  await page.reload();
  await expect(page.getByRole('link', { name: 'Open study' })).toBeVisible();
});
