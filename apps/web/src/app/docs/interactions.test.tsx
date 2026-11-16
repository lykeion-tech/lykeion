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
