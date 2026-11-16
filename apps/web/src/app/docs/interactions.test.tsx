import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { ConceptsDiagram } from './concepts-diagram';
import { StudyWalkthrough } from './study-walkthrough';
import { getArticle } from './content';

describe('interactive learning without JavaScript', () => {
  it('offers the four stages in order with native keyboard-selectable controls and complete text', () => {
    const html = renderToStaticMarkup(<StudyWalkthrough />);
    const names = ['Grounding', 'Hypotheses', 'Experimentation', 'Validation'];
    const labels = [...html.matchAll(/type="radio"[^>]*value="([^"]+)"/g)].map((match) => match[1]);
    expect(labels).toEqual(names);
    expect(html).toContain('<fieldset');
    for (const label of ['Inputs', 'Agents', 'Evidence', 'Gate', 'Result'])
      expect(html).toContain(label);
    expect(html).toContain('required review');
  });
  it('provides concept connections and registry-backed guide links', () => {
    const html = renderToStaticMarkup(<ConceptsDiagram />);
    for (const concept of [
      'Workspace',
      'Study',
      'Stages',
      'Agent',
      'Team',
      'Machine',
      'Run',
      'Evidence',
      'Skills',
      'Connectors',
      'Aristotle',
    ])
      expect(html).toContain(concept);
    for (const match of html.matchAll(/href="\/docs\/([^/]+)\/"/g))
      expect(getArticle(match[1])).toBeDefined();
    expect(html).toContain('Connected concepts');
  });
});

describe('reviewed learning behavior', () => {
  it('distinguishes claim review, measurement validation and return to experimentation', () => {
    const html = renderToStaticMarkup(<StudyWalkthrough />);
    for (const text of [
      'Without recorded runs',
      'quoted passages',
      'With recorded runs',
      'back to Experimentation once',
      'waits for its owner',
      'separate from an ordinary item redo',
    ])
      expect(html).toContain(text);
    expect(html).toContain('Method A versus method B');
    expect(html).toContain('aria-controls="stage-panel-3"');
    expect(html).not.toContain('href="/docs/scientific-stages/"');
  });
  it('derives both ends of every verified concept relationship from one pair', () => {
    const html = renderToStaticMarkup(<ConceptsDiagram />);
    const panel = (index: number) =>
      html.split(`id="concept-panel-${index}"`)[1].split('</div>')[0];
    expect(panel(0)).toContain('Agent');
    expect(panel(3)).toContain('Workspace');
    expect(panel(1)).toContain('Aristotle');
    expect(panel(10)).toContain('Study');
    expect(html).toContain('aria-controls="concept-panel-3"');
  });
});
