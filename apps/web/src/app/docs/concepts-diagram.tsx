import React from 'react';
import { getArticle } from './content';
import './learning.css';

import { concepts, connections, conceptNote } from './learning-content';

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
      <p className="learning-note">{conceptNote}</p>
    </section>
  );
}
