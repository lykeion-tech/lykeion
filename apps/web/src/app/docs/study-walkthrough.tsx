'use client';

import { useRef, useSyncExternalStore } from 'react';
import { stages, advancement, stageGuidance } from './learning-content';
import './learning.css';
import './stage-board.css';

const examples = [
  ['Sleep and memory: find the evidence', 'Map research on urban heat', 'Review evidence on air quality'],
  ['Compare explanations for memory retention', 'Explore causes of heat exposure'],
  ['Run a sampling simulation', 'Analyze temperature measurements', 'Compare two analysis methods', 'Reproduce a published computation'],
  ['Review a memory-retention claim', 'Check a temperature analysis', 'Examine a reproduced result'],
];
const subscribe = () => () => {};
const clientReady = () => true;
const serverReady = () => false;
export function StudyWalkthrough() {
  const dialogs = useRef<(HTMLDialogElement | null)[]>([]);
  const ready = useSyncExternalStore(subscribe, clientReady, serverReady);
  return <section className={`stage-board${ready ? ' stage-board-ready' : ''}`} aria-label="Study stage walkthrough">
    <p className="stage-board-caption">Explore the scientific stages</p>
    <div className="stage-board-scroll" tabIndex={0} role="region" aria-label="Scientific stage columns">
      <div className="stage-board-columns">
        {stages.map((stage, index) => <div className={`stage-board-column stage-board-column-${index}`} key={stage.name}>
          <button type="button" disabled={!ready} className="stage-board-select" data-stage={stage.name} aria-label={`Explore ${stage.name} stage`} aria-controls={`stage-dialog-${index}`} aria-haspopup="dialog" onClick={() => dialogs.current[index]?.showModal()}>
            <span className="stage-board-heading"><span className="stage-board-dot" aria-hidden="true" /><strong>{stage.name}</strong><span>{examples[index].length}</span><span className="stage-board-plus" aria-hidden="true">+</span></span>
            <span className="stage-board-cards">{examples[index].map((title, cardIndex) => <span className="study-example-card" key={title}><span>EXAMPLE-{examples.slice(0, index).reduce((sum, cards) => sum + cards.length, 0) + cardIndex + 1}</span><strong>{title}</strong></span>)}</span>
          </button>
          <dialog ref={node => { dialogs.current[index] = node; }} id={`stage-dialog-${index}`} className="stage-board-dialog" aria-labelledby={`stage-title-${index}`}>
            <form method="dialog"><button className="stage-board-close" aria-label="Close stage explanation">Close</button></form>
            <h2 id={`stage-title-${index}`}>{stage.name}</h2><p>{stageGuidance[index][0]}</p>
            <h3>Choose this stage when</h3><p>{stageGuidance[index][1]}</p>
            <dl>{[['Inputs', stage.input], ['Agents', stage.agents], ['Evidence', stage.evidence], ['Gate', stage.gate], ['Result', stage.result]].map(([label, text]) => <div key={label}><dt>{label}</dt><dd>{text}</dd></div>)}</dl>
            <p className="stage-board-advance">{advancement}</p>
          </dialog>
        </div>)}
      </div>
    </div>
    <div className="stage-board-fallback"><p>Enable JavaScript to open stage explanations.</p>{stages.map((stage, index) => <p key={stage.name}><strong>{stage.name}:</strong> {stageGuidance[index][0]} {stageGuidance[index][1]}</p>)}</div>
  </section>;
}
