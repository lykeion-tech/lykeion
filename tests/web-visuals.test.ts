import { readFileSync } from 'node:fs';
import { describe, expect, it, vi } from 'vitest';
import { parse, compileScript } from '@vue/compiler-sfc';
import ResearchSections from '../apps/web/src/ResearchSections.vue';
vi.mock('vue', async (original) => ({
  ...(await original<typeof import('vue')>()),
  useSSRContext: () => ({ modules: new Set() }),
}));
const source = readFileSync('apps/web/src/ResearchSections.vue', 'utf8');
describe('real website dashboard visuals', () => {
  it('changes both source and description across distinct cases and alternate views', () => {
    // Exercise the component's real reactive selection state without a browser renderer.
    const setup = (ResearchSections as any).setup({}, { expose() {} });
    const initial = setup.currentCapture.value;
    const sources = new Set<string>();
    for (let index = 0; index < setup.cases.length; index++) {
      setup.activeCase.value = index;
      setup.activeCapture.value = 0;
      const current = setup.currentCapture.value;
      sources.add(current.src);
      expect(current.alt.length).toBeGreaterThan(70);
      expect(current.alt).toContain('Lykeion Demo');
    }
    expect(sources.size).toBe(6);
    setup.activeCase.value = 0;
    setup.activeCapture.value = 1;
    expect(setup.currentCapture.value.src).not.toBe(initial.src);
    expect(setup.currentCapture.value.alt).not.toBe(initial.alt);
    for (const card of setup.cards.value) {
      expect(card.captures).toHaveLength(2);
      expect(card.captures[0].src).not.toBe(card.captures[1].src);
    }
  });
  it('maps all reviewed captures with descriptive alternatives', () => {
    for (const name of [
      'board',
      'study',
      'run',
      'evidence',
      'team',
      'experiment',
      'cowrite',
      'autoresearch',
      'autoresearch-list',
      'machines',
      'aristotle',
      'aristotle-evidence',
    ]) {
      expect(source).toContain(`/demo/${name}.webp`);
    }
    expect(source).toContain(':alt="currentCapture.alt"');
    expect(source).toContain(':src="currentCapture.src"');
    expect(source).toContain('cases[activeCase].captures');
  });
  it('provides native keyboard controls, selection, enlargement and reduced motion', () => {
    expect(source).toContain(':aria-pressed="activeCapture === index"');
    expect(source).toContain('@click="activeCapture = index"');
    expect(source).toContain('showModal()');
    expect(source).toContain('prefers-reduced-motion: reduce');
    expect(source).toContain('<Transition');
    expect(source).toContain('Lykeion Demo');
  });
  it('removes invented dashboard markup and compiles', () => {
    for (const name of ['mini-terminal', 'mini-evidence', 'board-columns', 'case-ui-body'])
      expect(source).not.toContain(name);
    expect(() => compileScript(parse(source).descriptor, { id: 'research' })).not.toThrow();
  });
});
