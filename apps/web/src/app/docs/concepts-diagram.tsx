import React from 'react';
import { getArticle } from './content';
import './learning.css';

const concepts = [
  {
    name: 'Workspace',
    slug: 'workspaces',
    text: 'The shared context for studies, colleagues and configuration.',
  },
  {
    name: 'Study',
    slug: 'studies',
    text: 'A research question or task with an assignee and configured stage scope.',
  },
  {
    name: 'Stages',
    slug: 'scientific-stages',
    text: 'Grounding, Hypotheses, Experimentation and Validation describe the process. A study can cover part of that sequence.',
  },
  {
    name: 'Agent',
    slug: 'agents',
    text: 'A reusable research role with instructions, capabilities and execution settings. Each run is an individual attempt.',
  },
  {
    name: 'Team',
    slug: 'teams',
    text: 'Coordinated research roles around a stage, with members, instructions and review criteria to inspect.',
  },
  {
    name: 'Machine',
    slug: 'connect-machine',
    text: 'A computer exposing agent CLI runtimes. Check the runtime is online and healthy before assigning execution.',
  },
  {
    name: 'Run',
    slug: 'runs',
    text: 'One execution attempt, with its own progress and errors. An agent profile is reusable across attempts.',
  },
  {
    name: 'Evidence',
    slug: 'evidence-and-results',
    text: 'Sources, hand-ins, files, notebook records and review to inspect against the question. An output alone does not establish a validated finding.',
  },
  {
    name: 'Skills',
    slug: 'skills',
    text: 'Reusable instruction packs selected in an agent or Team’s capabilities. They do not supply credentials or connect a runtime.',
  },
  {
    name: 'Connectors',
    slug: 'connectors',
    text: 'Configured access to tools or data sources. Inspect availability and the agent’s capability configuration.',
  },
  {
    name: 'Aristotle',
    slug: 'aristotle',
    text: 'Helps you ask about selected workspace material and follow evidence. Its answers do not complete a study’s required review.',
  },
];

// Each verified relationship is defined once and rendered from either end.
const relationships: [number, number][] = [
  [0, 1],
  [0, 3],
  [0, 4],
  [0, 8],
  [0, 9],
  [0, 10],
  [1, 2],
  [1, 3],
  [1, 4],
  [1, 6],
  [1, 7],
  [1, 10],
  [2, 3],
  [2, 4],
  [2, 7],
  [3, 4],
  [3, 5],
  [3, 6],
  [3, 8],
  [3, 9],
  [4, 8],
  [5, 6],
  [6, 7],
  [7, 10],
];
const connections = (index: number) =>
  relationships.flatMap(([a, b]) => (a === index ? [b] : b === index ? [a] : []));

// Use the grid gutters so a relationship never crosses an unrelated concept.
export function conceptPath(first: number, second: number) {
  const [source, target] = [first, second].sort((a, b) => a - b);
  const sx = 100 + (source % 3) * 200;
  const sy = 36 + Math.floor(source / 3) * 96;
  const tx = 100 + (target % 3) * 200;
  const ty = 36 + Math.floor(target / 3) * 96;
  if ((sy === ty && tx - sx === 200) || (sx === tx && ty - sy === 96))
    return `M ${sx} ${sy} L ${tx} ${ty}`;
  const gutter = sx === tx ? (sx === 100 ? 20 : sx - 100) : tx + (sx < tx ? -100 : 100);
  return `M ${sx} ${sy} V ${sy + 48} H ${gutter} V ${ty} H ${tx}`;
}

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
              {connections(index).map((target) => (
                <path
                  key={target}
                  d={conceptPath(index, target)}
                  data-source={index}
                  data-target={target}
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
              aria-controls={`concept-panel-${index}`}
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
              <p>
                {connections(index)
                  .map((target) => concepts[target].name)
                  .join(' · ')}
              </p>
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
