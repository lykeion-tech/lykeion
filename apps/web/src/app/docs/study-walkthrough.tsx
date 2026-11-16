import React from 'react';
import Link from 'next/link';
import { getArticle } from './content';
import './learning.css';

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
    gate: 'The server derives hypothesis statuses from assessments and reproduced measurements. Objections can lower the status; they cannot raise it.',
    result: 'A review record to assess against the original question and scope.',
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
              defaultChecked={index === 0}
            />
            <label className="stage-choice" htmlFor={`learn-stage-${index}`}>
              <span>0{index + 1}</span>
              {stage.name}
            </label>
            <div className="learning-panel stage-panel" id={`stage-panel-${index}`}>
              <div className="stage-scene" aria-hidden="true">
                <span className="scene-question">?</span>
                <i />
                <span className="scene-agent">{index === 3 ? 'REVIEW' : 'AGENT'}</span>
                <i />
                <span className="scene-evidence">
                  {['SOURCES', 'IDEAS', 'ARTIFACTS', 'CHECKS'][index]}
                </span>
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
        review passes, carrying unclear items when applicable. Failed items trigger one redo, then a
        block if still failing. Unavailable review services leave the study waiting. At the end of
        its scope, completion follows required review. An individual agent is restricted to its own
        stage.
      </p>
      <Link href="/docs/scientific-stages/">
        Read {getArticle('scientific-stages')!.title.toLowerCase()} →
      </Link>
    </section>
  );
}
