import { existsSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import { articles, getArticle } from './content';

const required = [
  'welcome',
  'quickstart',
  'core-concepts',
  'how-lykeion-works',
  'complete-tutorial',
  'workspaces',
  'members-and-roles',
  'studies',
  'scientific-stages',
  'decisions',
  'jev',
  'evidence-and-results',
  'workflows',
  'agents',
  'teams',
  'skills',
  'aristotle',
  'connect-machine',
  'runs',
  'troubleshooting',
  'coresearch',
  'autoresearch',
  'connectors',
];
describe('researcher guides', () => {
  it('publishes the complete initial inventory', () => {
    for (const slug of required) expect(getArticle(slug), slug).toBeDefined();
    expect(new Set(articles.map((a) => a.slug)).size).toBe(articles.length);
  });
  it('provides descriptions, substantive steps, success checks and deeper links', () => {
    for (const a of articles) {
      expect(a.description.length, a.slug).toBeGreaterThan(30);
      expect(a.markdown.length, a.slug).toBeGreaterThan(900);
      expect(a.markdown, a.slug).toMatch(/## /);
      expect(a.markdown, a.slug).toMatch(/\]\(\/docs\/[a-z-]+\/\)/);
    }
  });
  it('links only to existing articles and local screenshot assets', () => {
    for (const a of articles) {
      for (const [, slug] of a.markdown.matchAll(/\]\(\/docs\/([^/#)]+)\//g))
        expect(getArticle(slug), `${a.slug}: ${slug}`).toBeDefined();
      for (const [, path] of a.markdown.matchAll(/!\[[^\]]*\]\((\/docs\/[^)]+)\)/g))
        expect(existsSync(resolve('apps/web/public', `.${path}`)), path).toBe(true);
    }
  });
  it('takes both first-study journeys through setup, execution and review', () => {
    for (const slug of ['quickstart', 'complete-tutorial']) {
      const text = getArticle(slug)?.markdown ?? '';
      for (const word of ['workspace', 'machine', 'study', 'progress', 'results'])
        expect(text.toLowerCase(), `${slug}: ${word}`).toContain(word);
    }
  });
});

describe('hosted setup and actual study controls', () => {
  it('leads setup journeys with desktop and prebuilt CLI paths', () => {
    for (const slug of ['quickstart', 'complete-tutorial', 'connect-machine']) {
      const text = getArticle(slug)!.markdown;
      expect(text, slug).toContain('](/download/)');
      expect(text, slug).toContain('desktop app');
      expect(text, slug).toContain('prebuilt CLI');
      expect(text.indexOf('desktop app'), slug).toBeLessThan(
        text.indexOf('Go 1.26') < 0 ? text.length : text.indexOf('Go 1.26'),
      );
      expect(text, slug).not.toContain('Machine setup currently requires Go');
    }
    const machine = getArticle('connect-machine')!.markdown;
    expect(machine).toContain('lyk workspace switch <slug>');
    expect(machine).toContain('bubblewrap');
    expect(machine).toContain('## Alternative: build from source');
  });
  it('checks the machine outside the manual study composer', () => {
    const tutorial = getArticle('complete-tutorial')!.markdown;
    expect(tutorial).not.toContain('Check the selected machine and any required inputs');
    expect(tutorial).toContain('agent configuration');
    expect(tutorial).toContain('Check the assignee, stage scope and required inputs');
    expect(getArticle('troubleshooting')!.markdown).not.toContain(
      'its assignee, scope and selected machine',
    );
  });
  it('uses detail crops for status and research-record screenshots', () => {
    for (const [slug, asset] of [
      ['connect-machine', 'connected-machine-detail.jpg'],
      ['complete-tutorial', 'machine-runtimes-detail.jpg'],
      ['complete-tutorial', 'study-board-detail.jpg'],
      ['complete-tutorial', 'study-progress-detail.jpg'],
      ['evidence-and-results', 'study-progress-detail.jpg'],
    ])
      expect(getArticle(slug)!.markdown, slug).toContain(`/docs/${asset}`);
  });
});

it('does not present native Windows commands as supported agent execution', () => {
  const machine = getArticle('connect-machine')!.markdown;
  expect(machine).toContain('Native Windows agent execution is not supported');
  expect(machine).toContain('macOS or Linux execution machine');
  expect(machine).not.toContain('keep the foreground daemon terminal open');
  expect(machine).not.toContain('On Windows, read the foreground output');
});

it('checks runtime availability in Settings after choosing an agent or Team', () => {
  const concepts = getArticle('core-concepts')!.markdown;
  expect(concepts).not.toContain('Select a **machine**');
  expect(concepts).toContain('Settings → Machines');
  expect(concepts).toContain('runtime needed by your agent or Team is online');
  expect(getArticle('connect-machine')!.description).not.toMatch(/^Build/);
});

it('labels the actual output example as simulated and awaiting scoring', () => {
  const evidence = getArticle('evidence-and-results')!.markdown;
  expect(evidence).toContain('/docs/experiment-outputs-detail.jpg');
  expect(evidence).toContain('](/docs/experiment-outputs.jpg)');
  expect(evidence).toContain('DISC-2');
  expect(evidence).toContain('explicitly simulated data');
  expect(evidence).toContain('still waiting for unavailable scoring');
});

it('replaces the Projects guide with Jev and links Decisions to review', () => {
  expect(getArticle('projects')).toBeUndefined();
  expect(getArticle('jev')?.section).toBe('Research');
  expect(getArticle('decisions')?.section).toBe('Research');
  expect(getArticle('decisions')?.markdown).toContain('](/docs/jev/)');
  expect(getArticle('jev')?.markdown).toContain('](/docs/decisions/)');
});
