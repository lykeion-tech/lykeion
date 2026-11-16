import React from 'react';
import { getArticle } from './content';
import './learning.css';

const concepts = [
  {
    name: 'Workspace',
    slug: 'workspaces',
    text: 'The shared context for studies, colleagues and configuration.',
    links: [1, 3, 4, 8, 9, 10],
  },
  {
    name: 'Study',
    slug: 'studies',
    text: 'A research question or task with an assignee and configured stage scope.',
    links: [0, 2, 3, 4, 6, 7],
  },
  {
    name: 'Stages',
    slug: 'scientific-stages',
    text: 'Grounding, Hypotheses, Experimentation and Validation describe the process. A study can cover part of that sequence.',
    links: [1, 3, 4, 7],
  },
  {
    name: 'Agent',
    slug: 'agents',
    text: 'A reusable research role with instructions, capabilities and execution settings. Each run is an individual attempt.',
    links: [1, 2, 4, 5, 6, 8, 9],
  },
  {
    name: 'Team',
    slug: 'teams',
    text: 'Coordinated research roles around a stage, with members, instructions and review criteria to inspect.',
    links: [1, 2, 3, 8],
  },
  {
    name: 'Machine',
    slug: 'connect-machine',
    text: 'A computer exposing agent CLI runtimes. Check the runtime is online and healthy before assigning execution.',
    links: [3, 6],
  },
  {
    name: 'Run',
    slug: 'runs',
    text: 'One execution attempt, with its own progress and errors. An agent profile is reusable across attempts.',
    links: [1, 3, 5, 7],
  },
  {
    name: 'Evidence',
    slug: 'evidence-and-results',
    text: 'Sources, hand-ins, files, notebook records and review to inspect against the question. An output alone does not establish a validated finding.',
    links: [1, 2, 6, 10],
  },
  {
    name: 'Skills',
    slug: 'skills',
    text: 'Reusable instruction packs selected in an agent or Team’s capabilities. They do not supply credentials or connect a runtime.',
    links: [0, 3, 4],
  },
  {
    name: 'Connectors',
    slug: 'connectors',
    text: 'Configured access to tools or data sources. Inspect availability and the agent’s capability configuration.',
    links: [0, 3],
  },
  {
    name: 'Aristotle',
    slug: 'aristotle',
    text: 'Helps you ask about selected workspace material and follow evidence. Its answers do not complete a study’s required review.',
    links: [0, 1, 7],
  },
];

export function ConceptsDiagram() {
  return (
    <section className="learning concepts" aria-label="Core concepts diagram">
      <div className="learning-kicker">EXPLORE THE CONNECTIONS</div>
      <h2>A shared place for research.</h2>
      <p>
        Select a concept to trace its relationships. Use Tab to enter the map and arrow keys to
        explore.
      </p>
      <fieldset>
        <legend>Choose a concept</legend>
        <svg
          className="concept-lines"
          viewBox="0 0 600 360"
          preserveAspectRatio="none"
          aria-hidden="true"
        >
          {concepts.map((concept, index) => (
            <g key={concept.name} className={`concept-path concept-path-${index}`}>
              {concept.links.map((target) => (
                <path
                  key={target}
                  d={`M ${100 + (index % 3) * 200} ${36 + Math.floor(index / 3) * 96} L ${100 + (target % 3) * 200} ${36 + Math.floor(target / 3) * 96}`}
                />
              ))}
            </g>
          ))}
        </svg>
        {concepts.map((concept, index) => (
          <React.Fragment key={concept.name}>
            <input
              type="radio"
              className="learning-radio"
              name="concept"
              id={`learn-concept-${index}`}
              value={concept.name}
              defaultChecked={index === 1}
            />
            <label
              className="concept-choice"
              htmlFor={`learn-concept-${index}`}
              style={{ gridColumn: (index % 3) + 1, gridRow: Math.floor(index / 3) + 1 }}
            >
              <span>{String(index + 1).padStart(2, '0')}</span>
              {concept.name}
            </label>
            <div className="learning-panel concept-panel" id={`concept-panel-${index}`}>
              <h3>{concept.name}</h3>
              <p>{concept.text}</p>
              <h4>Connected concepts</h4>
              <p>{concept.links.map((target) => concepts[target].name).join(' · ')}</p>
              <a href={`/docs/${concept.slug}/`}>Read {getArticle(concept.slug)!.title} →</a>
            </div>
          </React.Fragment>
        ))}
      </fieldset>
      <p className="learning-note">
        Lines show documented relationships, not automatic data flow or guaranteed execution.
      </p>
    </section>
  );
}
