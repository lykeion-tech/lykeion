import React from 'react';
import './learning.css';

// Symbolic example: no measurements or scientific findings are asserted.
const scenes = [
  [
    'Source S',
    'SCREEN SOURCE',
    'Exists · not retracted · meets criteria',
    'Eligible evidence if checks pass',
  ],
  [
    'Evidence from S',
    'FORM PREDICTIONS',
    'Cited · falsifiable · not already tested',
    'A exceeds B / A does not exceed B',
  ],
  [
    'Compare A and B',
    'TRACE EXECUTION',
    'Saved output · values traced to run',
    'Measurement mA, mB + output file',
  ],
  [
    'Claim or mA, mB',
    'INDEPENDENT CHECK',
    'Source passage / reproduced measurement',
    'Claim verdict / status or return',
  ],
];

const stages = [
  {
    name: 'Grounding',
    verb: 'Find the foundation',
    input: 'A research question, boundaries and relevant literature or data.',
    agents: 'An assigned Grounding agent or Team searches and assesses sources.',
    evidence: 'Cited sources, relevance notes and reported access limitations.',
    gate: 'The server checks source existence and retraction; Jev screens sources against the question’s criteria and checks missed papers.',
    result: 'An inspectable foundation for the question; a hand-in alone is not a passed review.',
  },
  {
    name: 'Hypotheses',
    verb: 'Make alternatives explicit',
    input: 'The question and the available grounding evidence.',
    agents: 'An assigned Hypotheses agent or Team develops rival explanations.',
    evidence: 'Proposed hypotheses and the distinctions an experiment could test.',
    gate: 'The server checks recorded citations; Jev checks falsifiability, evidence support and whether the exact prediction was already tested.',
    result: 'Explicit alternatives to investigate, rather than a confirmed explanation.',
  },
  {
    name: 'Experimentation',
    verb: 'Put the question to work',
    input: 'The task, required data and a suitable analysis or workflow.',
    agents:
      'An Experimentation agent executes the assigned work on a usable machine. Workflow studies use an individual agent.',
    evidence: 'Produced files, computation, notebook records and reported errors.',
    gate: 'The server checks saved outputs and traces reported values to execution. Jev checks how traced values are described.',
    result: 'Inspectable analysis and artifacts, with limitations to carry into review.',
  },
  {
    name: 'Validation',
    verb: 'Challenge the findings',
    input: 'Findings and their supporting evidence and artifacts.',
    agents:
      'Independent reviewers test the findings; inspect the assigned roles and actual review record.',
    evidence: 'Review explanations, checks and unresolved objections.',
    gate: 'Without recorded runs, the server checks quoted passages against their sources and Jev judges claims independently of the Team’s verdict. With recorded runs, the server derives hypothesis statuses from assessments and reproduced measurements; upheld objections can lower them.',
    result:
      'With recorded runs, an invalid status sends the study back to Experimentation once. Another invalid result waits for its owner. This return is separate from an ordinary item redo. Without recorded runs, claim review follows the item-review outcomes.',
  },
];

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
        Illustrative question: does A change a measurement compared with B? Follow symbolic source S
        and measurements mA and mB; no result is assumed.
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
              <div className="stage-scene" aria-label="Illustrative evidence and gate">
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
      <p className="learning-note">
        After hand-in, a Team’s auditor settles before required review; an individual agent goes
        straight to review. The server advances automatically within the configured scope when
        review passes, carrying unclear items when applicable. Ordinary item-review failures trigger
        one redo, then a block if still failing. Unavailable review services leave the study
        waiting. At the end of its scope, completion follows required review. An individual agent is
        restricted to its own stage.
      </p>
      <a href="#read-the-stage-vocabulary">Read the stage vocabulary below ↓</a>
    </section>
  );
}
