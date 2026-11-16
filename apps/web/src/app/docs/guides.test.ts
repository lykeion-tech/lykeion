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
