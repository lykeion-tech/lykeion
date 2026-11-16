import React from 'react';
import './learning.css';

import { scenes, stages, advancement, example } from './learning-content';

export function StudyWalkthrough() {
  return (
    <section className="learning walkthrough" aria-label="Study stage walkthrough">
      <div className="learning-kicker">FOLLOW THE RESEARCH</div>
      <h2>One question. Four lenses.</h2>
      <p>
        Explore the stage vocabulary. Your study’s configured scope determines which stages run.
      </p>
      <p className="example-question">
        <strong>Method A versus method B</strong>
        <br />
        {example}
      </p>
      <fieldset>
        <legend>Choose a scientific stage</legend>
        <div className="stage-track" aria-hidden="true">
          <span>QUESTION</span>
          <span>EVIDENCE</span>
          <span>REVIEW</span>
        </div>
        {stages.map((stage, index) => (
          <React.Fragment key={stage.name}>
            <input
              className="learning-radio"
              type="radio"
              name="study-stage"
              id={`learn-stage-${index}`}
              value={stage.name}
              aria-controls={`stage-panel-${index}`}
              defaultChecked={index === 0}
            />
            <label className="stage-choice" htmlFor={`learn-stage-${index}`}>
              <span>0{index + 1}</span>
              {stage.name}
            </label>
            <div className="learning-panel stage-panel" id={`stage-panel-${index}`}>
              <div className="stage-scene" role="group" aria-label="Illustrative evidence and gate">
                <div className="scene-input">
                  <small>EVIDENCE IN</small>
                  <strong>{scenes[index][0]}</strong>
                </div>
                <span className="scene-arrow" aria-hidden="true">
                  →
                </span>
                <div className="scene-gate">
                  <small>{scenes[index][1]}</small>
                  <strong>{scenes[index][2]}</strong>
                </div>
                <span className="scene-arrow" aria-hidden="true">
                  →
                </span>
                <div className="scene-result">
                  <small>CHECKED OUTCOME</small>
                  <strong>{scenes[index][3]}</strong>
                </div>
              </div>
              <h3>{stage.verb}</h3>
              <dl>
                {[
                  ['Inputs', stage.input],
                  ['Agents', stage.agents],
                  ['Evidence', stage.evidence],
                  ['Gate', stage.gate],
                  ['Result', stage.result],
                ].map(([title, text]) => (
                  <div key={title}>
                    <dt>{title}</dt>
                    <dd>{text}</dd>
                  </div>
                ))}
              </dl>
            </div>
          </React.Fragment>
        ))}
      </fieldset>
      <p className="learning-note">{advancement}</p>
      <a href="#read-the-stage-vocabulary">Read the stage vocabulary below ↓</a>
    </section>
  );
}
