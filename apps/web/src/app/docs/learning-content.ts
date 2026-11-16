// Symbolic example: no measurements or scientific findings are asserted.
export const scenes = [
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

export const stages = [
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

export const advancement =
  'After hand-in, a Team’s auditor settles before required review; an individual agent goes straight to review. The server advances automatically within the configured scope when review passes, carrying unclear items when applicable. Ordinary item-review failures trigger one redo, then a block if still failing. Unavailable review services leave the study waiting. At the end of its scope, completion follows required review. An individual agent is restricted to its own stage.';
export const example =
  'Illustrative question: does A change a measurement compared with B? Follow symbolic source S and measurements mA and mB; no result is assumed.';
export const concepts = [
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
export const relationships: [number, number][] = [
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
export const connections = (index: number) =>
  relationships.flatMap(([a, b]) => (a === index ? [b] : b === index ? [a] : []));

export const conceptNote =
  'Lines show documented relationships, not automatic data flow or guaranteed execution.';

export function learningMarkdown(slug: string): string {
  if (slug === 'scientific-stages')
    return `## Stage walkthrough

**Method A versus method B**

${example}

${stages
  .map(
    (stage, index) => `### ${stage.name}: ${stage.verb}

${[
  ['Inputs', stage.input],
  ['Agents', stage.agents],
  ['Evidence', stage.evidence],
  ['Gate', stage.gate],
  ['Result', stage.result],
]
  .map(([label, text]) => `- **${label}:** ${text}`)
  .join('\n')}

Illustration: ${scenes[index].join(' → ')}.`,
  )
  .join('\n\n')}

${advancement}
`;
  if (slug === 'core-concepts')
    return `## Concept relationships

${conceptNote}

${concepts
  .map(
    (concept, index) => `### ${concept.name}

${concept.text}

Connected concepts: ${connections(index)
      .map((target) => concepts[target].name)
      .join(' · ')}.

[Read ${concept.name}](/docs/${concept.slug}/)`,
  )
  .join('\n\n')}
`;
  return '';
}
