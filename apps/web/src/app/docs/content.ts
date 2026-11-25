import { learningMarkdown } from './learning-content';

export type DocArticle = {
  slug: string;
  title: string;
  description: string;
  section: string;
  markdown: string;
  proseMarkdown?: string;
};

const authoredArticles: DocArticle[] = [
  {
    slug: 'welcome',
    title: 'Welcome to Lykeion',
    description: 'A research workspace where agents work and people steer.',
    section: 'Get started',
    markdown: `# Welcome to Lykeion

Lykeion brings studies, research agents and collaborators into a shared workspace. You set the question, choose the scope and inspect what agents produce. Start with a small question whose evidence you can check yourself.

## Open your workspace

1. [Sign in to Lykeion](/app/#/signin). Use the account that belongs to your lab.
2. Complete onboarding or select an existing workspace. The Laboratory step lets you create a workspace, choose its name and URL, and set a study card prefix.
3. Review Skills and Connectors, then connect a machine so agents have somewhere to execute.

Your workspace opens in Coresearch with the conversation sidebar expanded. To run agents, connect a machine with a supported, authenticated runtime and confirm it appears online.

After an update, Lykeion shows the new features when you return to your workspace. Open Settings → Updates → **What’s new** to review the current version’s highlights again.

## Choose your starting point

Follow the [quickstart](/docs/quickstart/) for a short first study, or the [complete tutorial](/docs/complete-tutorial/) for a more deliberate walkthrough. [Core concepts](/docs/core-concepts/) explains the difference between a study, an agent and a run.

## Check the outcome

Read the study's activity and generated files, then open Review to understand the evidence and checks available. An agent finishing its turn is a useful milestone; scientific conclusions still require inspection.

If you cannot see your lab, check the selected workspace before creating another. If work is waiting, start with [machine connection](/docs/connect-machine/) and [troubleshooting](/docs/troubleshooting/).

[Browse the documentation](/docs/) to return to the guide index.
`,
  },
  {
    slug: 'quickstart',
    title: 'Your first study',
    description:
      'Set up a workspace and machine, start a bounded study, and inspect its first evidence.',
    section: 'Get started',
    markdown: `# Your first study

Start with a modest literature question, for example: How does sleep affect memory consolidation? You need a Lykeion account and a computer with an authenticated supported agent CLI.

## 1. Open a workspace

Sign in and follow onboarding. Add your research profile after Welcome, then choose an existing workspace or create one with a name, URL and card prefix. Choose whether to publish your profile to that workspace. The Customize step brings Skills and Connectors together. Keep the default laboratory for your first attempt unless your lab has instructed otherwise.

## 2. Connect a machine

Use the [download page](/download/) to install the desktop app, which includes the daemon. Sign in to your lab account and install and authenticate Claude Code or Codex on that computer. For a remote computer, use the prebuilt CLI instructions on the same page, choose your workspace and start the daemon. Linux also needs bubblewrap. Open Settings → Connections → Machines to confirm the computer and intended runtime are online. See [connect a machine](/docs/connect-machine/) for the complete setup.

## 3. Create the study

Open Studies and create a new study. Enter a short title and a description with your question, boundaries and requested output. Choose an available Grounding agent or a suitable Team and check the stage scope. For a small literature exercise, request a cited evidence summary and explicitly ask the agent to report inaccessible sources. Submit with Create study.

![A populated New study form in the actual demonstration workspace](/docs/new-study-form.jpg)

## 4. Follow progress

Open the new study. Read Activity and expand agent cards to follow execution. If it waits, inspect the selected machine, runtime health and reported error before submitting another study. Stage progress and review status are separate from a single agent's completion.

## 5. Inspect results

Read the hand-in and check its cited sources. Open Files for generated artifacts, Notebook for recorded computation and Review for available checks. An empty surface can mean that this work has not produced that kind of output yet.

You have completed this quickstart when you can identify the study, its assigned runtime and its first inspectable hand-in or a concrete blocker. A study reaches Done only after its configured stages and required review finish. Continue with the [complete tutorial](/docs/complete-tutorial/) and [evidence and results](/docs/evidence-and-results/).
`,
  },
  {
    slug: 'core-concepts',
    title: 'Core concepts',
    description:
      'Understand how workspaces, studies, stages, agents, machines and evidence fit together.',
    section: 'Get started',
    markdown: `# Core concepts

Before assigning research, separate the question you want answered from the software that executes it. You need only a signed-in workspace to browse these objects; executing work also needs a machine and a usable agent runtime.

## Follow one piece of work

1. Choose a **workspace**, the shared context for your lab's studies, colleagues and configuration.
2. Create a **study**, a question or task with a description, assignee and stage scope.
3. Assign an **agent**, a configured research role, or a **Team**, a coordinated group for a stage.
4. Open Settings → Connections → Machines to confirm the runtime needed by your agent or Team is online. A **machine** is the computer exposing those runtimes; a **run** records one execution attempt.
5. Inspect the resulting activity, hand-ins, files, notebook records and review.

A machine can expose more than one runtime. An agent's identity is reusable across work; its individual runs have their own progress and errors.

## Use the scientific stages

The stage vocabulary is Grounding, Hypotheses, Experimentation and Validation. A study's configured scope determines which stages it covers. Stage labels describe the research process; they are not evidence that a particular result is correct. See [scientific stages](/docs/scientific-stages/).

## Add reusable support

Skills carry working instructions and supporting material. Connectors provide access to configured tools or data sources. Workflows package a specific experimental task. Aristotle helps you ask about workspace material and follow evidence. These serve different purposes; adding a skill does not connect a machine or supply database credentials.

## Check your understanding

Open an existing study and identify its question, assignee, stage and most recent run. Then find a hand-in or a reported blocker. If you cannot identify the execution computer, use [machines](/docs/connect-machine/); if you cannot tell what was produced, use [evidence and results](/docs/evidence-and-results/).
`,
  },
  {
    slug: 'how-lykeion-works',
    title: 'How Lykeion works',
    description:
      'Follow the path from an assigned question through machine execution to reviewable research records.',
    section: 'Get started',
    markdown: `# How Lykeion works

Lykeion connects a shared research workspace to computers that execute agent work. The hosted dashboard lets you describe work and inspect its history. Execution depends on a connected machine and its agent CLI, so a browser session alone does not mean research is running.

## Trace the handoff

1. A researcher creates a study with a question, inputs, assignee and scope.
2. Lykeion schedules execution for the selected agent or Team on an eligible runtime.
3. The machine's daemon carries out that work through its configured agent CLI.
4. Runs write progress, hand-ins and other outputs back to the study.
5. Required stage work and review determine whether the study can continue or finish.

Use [runs](/docs/runs/) to distinguish queued work, active execution and an ended attempt. Keep the selected machine online and its CLI signed in while work needs it.

## Read state at the right level

A finished run means that execution attempt ended. It does not by itself mean that the whole study is Done. A study may have more configured stages or required review outstanding. Read its stage, activity and Review surface together before treating the result as complete.

The demonstration study used for these guides produced real Grounding hand-ins but waited for scoring. Its screenshots show that intermediate state, rather than a successful end-to-end validation.

## Verify one handoff yourself

Open a study, find its latest agent card and identify the reported outcome. Check Files or Notebook where relevant, then inspect Review. You should be able to point to an actual record or artifact that supports what the agent says it delivered.

If no run starts, check [machine connection](/docs/connect-machine/) before changing the research question. If a run ended with an error, read the error and address its cause before retrying. [Evidence and results](/docs/evidence-and-results/) explains the inspection surfaces.
`,
  },
  {
    slug: 'complete-tutorial',
    title: 'Complete first-study tutorial',
    description:
      'Work through workspace setup, machine connection, study creation, progress and evidence review.',
    section: 'Get started',
    markdown: `# Complete first-study tutorial

This tutorial takes you from an account to an inspectable research hand-in. Use demonstration data and a narrow question. Allow time for agent execution and review; completion depends on available runtimes, source access and review services.

## Prepare

You need a signed-in Lykeion account, permission to use or create a workspace, and a computer with a supported authenticated agent CLI. The [download page](/download/) offers the desktop app with its daemon and a prebuilt CLI for remote computers. You do not need a source checkout for these hosted setup paths. If your lab supplies a connected machine, ask which runtime you may use.

For the example, ask: How does sleep affect memory consolidation? Request a sourced account of current evidence, competing explanations and limits. Do not upload confidential participant data for a first exercise.

## 1. Set up your workspace

1. Follow the Welcome screen to Laboratory.
2. Choose Create a new workspace, enter a name and URL, and review the study card prefix.
3. Select your laboratory configuration and continue through Skills and Connectors.
4. Confirm you are in the intended workspace before creating work.

![Actual Laboratory onboarding with a draft Research Lab workspace](/docs/workspace-setup.jpg)

This capture shows the setup form, before submitting another workspace. Existing members can select their lab instead. See [workspaces](/docs/workspaces/).

## 2. Connect execution

Install the desktop app from the [download page](/download/) for your own computer, or follow its prebuilt CLI instructions for a remote macOS / Linux machine. Install and authenticate Claude Code or Codex there; Linux also needs bubblewrap. For the CLI, sign in to Lykeion, choose your workspace and start the daemon. Then open Settings → Connections → Machines to check runtime availability. See [connect a machine](/docs/connect-machine/) for the steps; the source-build dialog is an alternative. Keep credentials in the terminal flow; do not paste them into a study.

[![Actual Claude Code and Codex runtime rows marked Online](/docs/machine-runtimes-detail.jpg)](/docs/machine-runtimes.jpg)

Both demonstration runtimes show Online under Health. Open the image for the full runtime view.

Check that the desired runtime is online and usable. A machine's presence in the list does not establish that every CLI on it is authenticated or healthy.

## 3. Describe and assign a study

1. Open Studies and create a study.
2. Use the example question as the title. In the description, request citations, competing explanations and a list of inaccessible sources.
3. Choose an appropriate agent or Team and review the stage scope. For your first exercise you can keep the scope bounded to Grounding.
4. Check the assignee, stage scope and required inputs, then select Create study. Check a bound machine in the agent configuration, or availability in Settings → Connections → Machines; the manual study composer has no machine selector.
5. Record the resulting study identifier so you can return to it.

Use [studies](/docs/studies/) for assignment details and [Teams](/docs/teams/) for coordinated stage work.

## 4. Follow progress

Open the study from its board card. Read Activity, expand agent cards and look for a hand-in, a running attempt or an explicit blocker. Check Review before assuming the stage has passed.

[![DISC-1 Grounding card waiting for unavailable scoring](/docs/study-board-detail.jpg)](/docs/study-board.jpg)

The demonstration produced real hand-ins, but scoring was unavailable. This is a blocked intermediate state; it does not verify later stages. If you see the same condition, resolve the reported service issue rather than treating the study as Done.

## 5. Review results

When the study has a final report, select **Go to the report** in Activity to open its final hand-in and evidence graph. The action appears only for a finished study with a stored report; unfinished or stopped studies, Workflow runs and older studies without a report do not show it. The local demonstration studies are waiting for scoring, so they do not yet have a finished report. Report access is described from the implementation; it was not verified live for these studies.

Read the hand-in and follow its source references. Compare the answer with your requested scope. Open Files for produced artifacts, Notebook for recorded computations and Review for available checks and their explanations. These surfaces can be empty when that kind of record has not been produced.

[![Searcher Grounding Team hand-in identity and Assignment heading](/docs/study-progress-detail.jpg)](/docs/study-progress.jpg)

This detail identifies the Searcher hand-in. Open the image for the full study screenshot and its assignment and findings.

This is a hand-in to inspect, not a passed validation. Record what the evidence supports, what remains unresolved and whether another stage is needed. See [evidence and results](/docs/evidence-and-results/).

## Finish the exercise

You should now be able to find your workspace, identify the execution machine, reopen the study and inspect a hand-in or explain its blocker. A full study is complete only when every configured stage and required review finishes. The pictured demonstration is waiting for scoring after its Grounding hand-ins. Read the actual state of your study before continuing to later stages. Use [troubleshooting](/docs/troubleshooting/) when your observed state differs.
`,
  },
  {
    slug: 'workspaces',
    title: 'Workspaces',
    description:
      'Create or select the shared laboratory context for your studies and collaborators.',
    section: 'Workspace',
    markdown: `# Workspaces

A workspace holds the shared context for your lab's research. Choose the right workspace before creating a study or changing configuration. You need an account; joining someone else's workspace also requires their invitation or existing membership.

## Create or select a workspace

1. In onboarding, open the Laboratory step. Existing memberships appear as choices.
2. Select your lab, or choose Create a new workspace.
3. For a new workspace, enter its name and URL. Review the card prefix used for study identifiers; you can enter a prefix explicitly.
4. Select the laboratory configuration and continue through the setup steps.

![The actual workspace setup form, with an unsubmitted Research Lab draft](/docs/workspace-setup.jpg)

Confirm the workspace name in the dashboard before continuing. The example capture is a draft form; it does not show a second workspace being created.

## Manage workspace details

Open Settings and the workspace's General section to inspect its details. Administrative controls depend on your role. Ask an owner or admin to make changes when you do not have the required access. See [members and roles](/docs/members-and-roles/).

Skills and Connectors are configured in their own workspace settings sections. A connected machine is execution infrastructure; creating a workspace does not automatically install or authenticate agent CLIs on your computer.

## When something is missing

If expected studies or colleagues are absent, check the selected workspace and account first. Do not create a duplicate workspace to recover access. Ask the lab's administrator to confirm your membership.

`,
  },
  {
    slug: 'members-and-roles',
    title: 'Members and roles',
    description:
      'Invite colleagues, inspect their workspace role and manage access with the right permissions.',
    section: 'Workspace',
    markdown: `# Members and roles

Colleagues are the people in your selected workspace. Workspace roles are Owner, Admin and Member. Before inviting someone or editing access, confirm that you are in the correct lab and that you have permission to administer it.

## Find a colleague

1. Open Colleagues in the dashboard.
2. Search or use the role filter to locate the person.
3. Read their career stage and workspace access in the roster, or open their details to inspect their profile. Their Studies tab shows visible studies owned by or assigned to them.

This view helps you identify who is responsible for work without changing its assignment. A person's workspace membership and an agent's execution configuration are separate. See [agents](/docs/agents/).

## Share your research profile

During onboarding, choose your **Career stage**, describe your research in your own words in the **Professional background** field and optionally add links to your work. You can choose whether to publish the profile to the workspace you join or create. Colleagues can read the published version in your Overview, and Aristotle can use it in Coresearch when suggesting collaborators.

In Settings → Profile, you can edit your career stage, research identity and links. Your saved career stage is visible in the Colleagues roster to people who share a workspace with you. **Save profile** updates the published version in workspaces where you’ve already shared it. If you haven’t shared it anywhere, the saved profile stays private.

## Invite someone

Owners and admins see the Invite control. Choose Invite, enter the colleague's email, select Member or Admin, and submit only after checking the address and intended role. The recipient needs to accept the invitation with the intended account. Pending invitations appear separately from existing members.

Confirm success in the invitations list, then check membership after acceptance. If the invitation cannot be accepted, ask the sender to verify the address and workspace rather than sharing another person's account.

## Manage access

Open the colleague's workspace access section to inspect available role controls. Ownership changes require an owner. The application protects the last owner from leaving the workspace. Give each colleague only the role their work needs.

If a control is absent, first check your role and selected workspace. A failed access change must be resolved before assuming the displayed permissions have changed. Ask an owner when you cannot complete the action yourself.

`,
  },
  {
    slug: 'studies',
    title: 'Studies',
    description:
      'Describe a research task, choose its stage scope and assignee, and follow the resulting work.',
    section: 'Research',
    markdown: `# Studies

A study is the place to describe a research question and follow its execution and evidence. Before creating one, choose the correct workspace, prepare the inputs you may share and confirm that the intended execution machine is available.

## Create a useful study

1. Open Studies and create a new study.
2. Enter a short title and a description of the question, relevant context, boundaries and expected deliverable.
3. Choose an assignee and review the stage scope. A Team's configured stage affects the choices available.
4. Attach any required inputs and wait for uploads to finish. Select a project or priority when useful.
5. Review the configuration and select Create study. Reopen the resulting card to confirm its identifier and assignment.

![Actual populated study creation form before submission](/docs/new-study-form.jpg)

For a first attempt, prefer a small question with a deliverable you can inspect. If you use Create with agent, inspect what it produces rather than assuming every requested property was applied.

To add a study you own to a workspace project, open the card's three-dot menu on the board and choose **Add to project**. Search for an existing project or choose **New project** to create and link one. You can also move the study to another project or remove the link.

## Save a study for later

Open **New study** and select **To-do** from the stage picker. Describe the work, choose one stage and an agent or Team, and optionally select a project. **Save to To-do** shares the proposal with the workspace. Your agent starts work only after you open the proposal and select **Start study**. Until then, you can edit or discard it.

Board and List show To-do, Grounding, Hypotheses, Experimentation, Validation, Done and Blocked in that order. **My Studies** shows your proposals. A project's Studies tab shows the proposals linked to it. Use **New study** for attachments, workflows or a scope covering multiple stages.

## Follow and steer work

Read Activity and expand agent cards to inspect execution and hand-ins. Comments on agent cards keep follow-up discussion attached to that work. Check the current stage and Review surface before interpreting an ended run as a finished study.

A study moves to Done only after all of its configured stages and required review finish. If review is waiting or a runtime is unavailable, resolve that state rather than reporting the scientific process as complete. See [scientific stages](/docs/scientific-stages/) and [runs](/docs/runs/).

## Inspect the deliverable

Use Files for generated artifacts, Notebook for recorded code and outputs, and Review for available checks and evidence. Compare the result with the original description and follow source references.

If creation is unavailable, check missing inputs, assignment, stage selection and upload errors. If creation succeeded but execution does not start, inspect [machine connection](/docs/connect-machine/). Use [evidence and results](/docs/evidence-and-results/) for a systematic review.
`,
  },
  {
    slug: 'scientific-stages',
    title: 'Scientific stages',
    description:
      'Understand the four research stages and inspect progress without confusing hand-ins with review.',
    section: 'Research',
    markdown: `# Scientific stages

Lykeion presents research in four stages: Grounding, Hypotheses, Experimentation and Validation. A study can cover a configured part of that sequence. Start by checking its scope and assignee; do not infer that every study will execute all four stages.

## Follow a study through its scope

1. Open the study and inspect its current stage and configured range.
2. Read the agent or Team activity for that stage.
3. Open Review to inspect available checks and explanations.
4. Look for the next observed state, or the concrete reason work is waiting.

A completed hand-in is not sufficient evidence that a stage passed. A study moves to Done only after every configured stage and required review finishes. If scoring or another required service is unavailable, preserve that distinction in your own notes.

## Plan what Validation will check

For a directional hypothesis such as “the filter reduces mean error,” plan a statistical test before Experimentation and have the experiment script record the test statistic and p-value. Validation can reproduce a directional result and still mark it inconclusive when the test is missing.

For a deterministic check, specify a named outcome or numeric range in Hypotheses and have the script record the result. Validation checks that the recorded result reproduces and matches the prediction.

## Check evidence before continuing

Compare the study's outputs with its question and scope. Use [evidence and results](/docs/evidence-and-results/) to inspect files, notebook records and review. The demonstration study in these guides has Grounding hand-ins and is waiting for scoring.

If you need to choose a stage-specific assignee, read [Teams](/docs/teams/) and [studies](/docs/studies/). Check the chosen stage against the question and the deliverable you need.
`,
  },
  {
    slug: 'decisions',
    title: 'Decisions',
    description: 'Review questions, respond to study blockers and inspect reports in Decisions.',
    section: 'Research',
    markdown: `# Decisions

Decisions brings together questions and other items that need your attention. Open it to review the context, see what a study needs and respond when you have permission. Finished study reports are also available from this area.

## Find the item

Select an item in the Decisions sidebar. Use the type and date filters to narrow the list, or clear them if an expected item is hidden. Open a question to see its status, its context and any available choices. When the item belongs to a study, **Open study** takes you to the research record.

## Answer a question

Read the question and the consequences of each option before choosing. Some questions contain several prompts, including multiple-choice or text answers; complete the required prompts before submitting. For an instruction to redo a blocked study, add the requested guidance. Only an authorized person can submit a study decision; the screen explains when you cannot answer.

A submitted answer is recorded with the decision. The study can then continue according to the chosen option; check its activity and current state rather than assuming a run has completed. An item may also have an expiry or a default, so inspect its details before waiting.

## Inspect a report or a private note

Select a report to read the hand-in, explore its evidence graph and open the Report pane. Use its tabs and source details to follow what the study recorded. A report is a starting point for inspection, not a substitute for checking the evidence.

When Validation has recorded findings, the report header summarizes their final outcomes. The recorded agent hand-in below preserves the agent's account from the time it was submitted.

Decisions can also contain private notes you create for yourself. Review a note before saving it, and use its controls to edit or resolve it. See [evidence and results](/docs/evidence-and-results/) for reading research outputs and [Jev](/docs/jev/) for how configured study review works.
`,
  },
  {
    slug: 'evidence-and-results',
    title: 'Evidence and results',
    description:
      "Inspect hand-ins, files, notebook records and review before using a study's conclusions.",
    section: 'Research',
    markdown: `# Evidence and results

Start from a study you can access in the selected workspace. The useful result is the material you can inspect, together with its limits. A confident response or a finished execution attempt alone does not establish a scientific conclusion.

## Inspect the research record

1. Read the study description to recover its question and requested deliverable.
2. Open Activity and expand the relevant agent card. Read the hand-in, its source references and any errors.
3. Open Files to browse generated artifacts and attached material. Search or switch between grid and list views when the collection grows.
4. Open Notebook for recorded computation, including code, printed output and standard error where present.
5. Open Review for the available checks and their explanations. Select a stage or check to inspect its details.

For a failed Validation replay, open **Full log** on the rerun card in Activity to see the script's exit status and any recorded standard error.

[![Searcher Grounding Team hand-in identity and Assignment heading](/docs/study-progress-detail.jpg)](/docs/study-progress.jpg)

The detail identifies the hand-in. Open the image for the full assignment and findings in the actual study screenshot.

The example shows a Searcher hand-in. Grounding scoring was unavailable, so this capture is not a passed review or a validated finding.

## Open a final report

When the study has a final report, select **Go to the report** in Activity to open its final hand-in and evidence graph. The action appears only for a finished study with a stored report; unfinished or stopped studies, Workflow runs and older studies without a report do not show it. The local demonstration studies are waiting for scoring, so they do not yet have a finished report. Report access is described from the implementation; it was not verified live for these studies.

## Inspect recorded output files

The DISC-2 demonstration study runs a sampling simulation using explicitly simulated data. Its Files surface contains recorded outputs, including a plot and a CSV. Open a file to inspect it and compare it with the study's stated method and inputs.

[![DISC-2 Files surface with a simulated-data plot and CSV output](/docs/experiment-outputs-detail.jpg)](/docs/experiment-outputs.jpg)

This detail is from the actual study; open the image for the full dashboard view. The files show what was produced from simulated data. DISC-2 is still waiting for unavailable scoring, so the presence of these files does not mean its review gate passed or that the result is a validated finding.

## Check before reusing a result

Follow cited sources and compare them with the claims being made. Check whether an artifact actually exists and whether the recorded analysis corresponds to the question and inputs. Note missing sources, failed computation and unresolved objections in your own interpretation.

Review displays the records available for that study. Older studies may show legacy review history. Read the labels and explanations rather than assuming scores from different review formats are directly comparable.

## Handle missing material

An empty Files or Notebook view can mean the work did not produce that type of record. Read Activity first. A loading error is different: use the retry control and preserve the error if it persists. If review is waiting, inspect the reported blocker.

You are ready to use an output when you can locate it, trace its relevant evidence and explain what remains uncertain. See [scientific stages](/docs/scientific-stages/) for completion and [troubleshooting](/docs/troubleshooting/) for execution problems.
`,
  },
  {
    slug: 'workflows',
    title: 'Workflows',
    description:
      'Inspect a packaged experimental workflow and start a study with the inputs it requires.',
    section: 'Research',
    markdown: `# Workflows

A workflow packages instructions and files for a particular experimental task. Lykeion's workflows are ported from [Terminal-Bench-Science](https://github.com/harbor-framework/terminal-bench-science). Use one when its documented inputs and purpose match your question. You need access to the workspace, the required input files and a suitable Experimentation agent on a usable machine.

## Inspect before starting

1. Open Workflows and select the relevant workflow.
2. Read README for its purpose, facts and prerequisites.
3. Open Instruction to inspect what the execution is asked to do.
4. Browse Files and search for supporting material when needed.

Check data requirements and expected outputs before assigning work. A workflow title is not enough to establish that your dataset or scientific question is appropriate.

## Start a workflow study

Choose Start study with this workflow. The study composer retains the selected workflow and uses Experimentation scope. Select a suitable agent, describe your task and attach the required input files. Wait for uploads to finish, then review and submit the study.

The workflow creation path expects an individual agent rather than a Team assignment. If the composer drops an incompatible assignee, choose an Experimentation agent and check the remaining requirements. See [studies](/docs/studies/) and [agents](/docs/agents/).

## Verify the output

Open the created study and confirm that the workflow and inputs match your intent. Follow Activity for execution and inspect Files and Notebook for produced artifacts and computation. Read Review where available before using the outcome.

If creation is disabled, check the selected agent and missing input files. If execution fails, preserve the specific error and inspect the machine and input compatibility before retrying. This guide explains the product flow; it does not certify any workflow's scientific suitability for a new dataset. Use [evidence and results](/docs/evidence-and-results/) to review the work.
`,
  },
  {
    slug: 'agents',
    title: 'Agents',
    description:
      'Choose a research agent by its instructions, stage, capabilities and execution machine.',
    section: 'Agents',
    markdown: `# Agents

An agent is a reusable research role with instructions and execution settings. Its runs are individual attempts at work. Before assigning a study, inspect the agent's stage, capabilities and machine rather than relying on its name alone. You need workspace access and permission to use that agent.

## Choose an agent

1. Open Agents and select a relevant agent.
2. Read Instructions to understand its responsibilities and delivery expectations.
3. Inspect Properties for its stage, execution harness and bound machine where shown.
4. Review Customize for the capabilities it can use, including relevant skills and connectors.
5. Check recent work, then choose it as the study assignee when its role matches the task.

An agent with no stage needs an appropriate stage selection before stage-specific assignment can work. A workflow study requires an Experimentation agent. See [studies](/docs/studies/) and [workflows](/docs/workflows/).

## Edit with a concrete purpose

If you have edit access, use the agent's settings to adjust its instructions, stage or execution configuration. For a custom agent, set a Study report template in General settings to format its hand-in answer. The default template shows the reporting instructions Lykeion gives the agent for its stage and role. Custom instructions apply to new runs. Required evidence and review checks still apply. Keep instructions specific: describe the task boundary, required evidence and expected deliverable. Add a reusable [skill](/docs/skills/) when the same working method belongs on several agents.

Access settings control who can assign studies to the agent. Check those settings and the machine's availability when another colleague cannot use it. Some machine configurations only execute their owner's studies; read the restriction shown by the product.

## Check the first assignment

After creating a small study, open its Activity and inspect the actual hand-in or error. Confirm that the selected role produced what you expected. An agent profile remaining visible does not mean its machine is online.

If execution waits, inspect [machine connection](/docs/connect-machine/) and [runs](/docs/runs/). For multiple coordinated roles in a stage, inspect [Teams](/docs/teams/) before assembling duplicate individual work.
`,
  },
  {
    slug: 'teams',
    title: 'Teams',
    description:
      'Inspect coordinated research roles, stage assignments and review criteria before assigning a Team.',
    section: 'Agents',
    markdown: `# Teams

A Team groups coordinated research roles around a stage. Inspect its members, instructions and review criteria before assigning a study. You need workspace access, permission to use the Team and a usable execution machine.

## Inspect a Team

1. Open Teams and choose a Team relevant to your question.
2. Read its stage and description.
3. Inspect the leader and member roles to understand the work each is expected to contribute.
4. Read the available details, instructions, skills and review criteria.
5. Check access before asking colleagues to use it.

Built-in Teams are already available in a workspace and cannot be edited directly. Other Teams may need Add Team before use. If you need a variation, use the supported duplication flow and inspect the resulting configuration.

## Assign stage work

Choose the Team in a new study's assignee picker. Confirm the stage selection: a single-stage Team supplies its own stage, while a broader assignment can require you to choose stages explicitly. Do not leave an unstaged Team's destination ambiguous. See [studies](/docs/studies/).

After submission, inspect Activity to see the actual members' contributions and hand-ins. A Team name alone does not establish that all its work has completed or passed review. The study's configured scope and required review still govern completion.

## Check capability availability

Team skills provide reusable instructions; they do not supply credentials or create a connected runtime. If a needed capability is unavailable, inspect [skills](/docs/skills/), [connectors](/docs/connectors/) and the selected machine before retrying.

You have a suitable assignment when the Team's stage and roles match the study and its first run produces an inspectable hand-in or a clear blocker. For a task designed around a packaged Experimentation workflow, follow [workflows](/docs/workflows/) and its individual-agent assignment requirements.
`,
  },
  {
    slug: 'skills',
    title: 'Skills',
    description:
      'Inspect and reuse instruction packs for the research methods your agents perform.',
    section: 'Data',
    markdown: `# Skills

A skill is an instruction pack agents can use while working. Its main file is SKILL.md, with supporting files where needed. Use skills for reusable methods, such as a literature screening procedure, rather than copying the same procedure into every study. You need access to the workspace's Skills settings.

## Inspect available skills

1. Open Settings and choose Skills under the workspace.
2. Search the library and open the relevant skill.
3. Read its instructions and inspect supporting files before enabling it for research.
4. Check the agent or Team's capability configuration to confirm that the intended skill is selected for that work.

A library entry and an agent's selected capabilities are different things. Confirm both before relying on a method during execution. See [agents](/docs/agents/) and [Teams](/docs/teams/).

## Add a reusable method

Choose Add skill. The available flows include starting from a blank SKILL.md, importing local material or importing from a URL. Local import accepts a folder containing SKILL.md or a supported archive. Read the preview and any validation error before saving.

Keep the method focused: explain when to use it, the required inputs, ordered steps and the output to retain. Include reference material only when it supports the method. Review imported instructions and scripts before making them available to agents.

## Verify that it helps

Use a small study to check whether the agent follows the intended method and produces the requested evidence. Inspect the hand-in and artifacts rather than assuming a successful import proves correct execution.

If a skill is missing from a picker, check the selected workspace, capability availability and import status. If the method needs external data or tools, configure the relevant [connector](/docs/connectors/); skill instructions alone do not supply credentials or a working runtime. Use [runs](/docs/runs/) to diagnose execution errors.
`,
  },
  {
    slug: 'aristotle',
    title: 'Ask Aristotle',
    description:
      'Ask about workspace material, attach relevant context and follow the evidence behind a response.',
    section: 'Agents',
    markdown: `# Ask Aristotle

Aristotle is Lykeion's research coordinator. Use the Ask Aristotle window to ask about relevant workspace material and follow the evidence behind its response. You need a workspace and an available supported runtime on your account for execution.

Aristotle reads studies in every column, whether finished or in progress, and proposals saved to To-do. If a study it read moves on, the answer stays and says so. It becomes unavailable if something it used is deleted, rewritten or no longer shared with you.

## Ask with context

1. Open Ask Aristotle in the dashboard.
2. Check the selected context. When you open it from a study, the current study can supply context.
3. Use Add a study, record or file to choose more relevant material, or remove context that does not belong in the question.
4. Select an available CLI and model where appropriate. The picker reflects your online, healthy runtimes.
5. Ask a bounded question, such as which sources support a particular statement in the study.

Do not assume that mentioning an inaccessible document makes it available. Attach or select the intended context and inspect what the response actually cites.

## Inspect the response

Turn on Show evidence to follow supporting material. Open the referenced study, record or file and check that it supports the claim you are evaluating. Ask a follow-up when a source is missing, a claim is too broad or the answer does not distinguish evidence from inference.

A useful answer helps you locate and interpret research material. It does not by itself mark a study's stages complete or replace its required review. See [evidence and results](/docs/evidence-and-results/).

## Keep conversations organized

Use Switch chat to return to a conversation or start a new one for a different question. Review the context when you move between studies so that you do not accidentally ask about the previous task.

If no model is available, inspect [machine connection](/docs/connect-machine/) and verify your own CLI is online and healthy. If a response has no useful evidence, narrow the question and select the specific material you want it to inspect. For a shared research project with files and ongoing work, see [Coresearch](/docs/coresearch/).
`,
  },
  {
    slug: 'connect-machine',
    title: 'Connect a machine',
    description:
      'Connect the desktop app or prebuilt CLI and confirm an available agent runtime.',
    section: 'Machine and execution',
    markdown: `# Connect a machine

A machine is a computer whose Lykeion daemon exposes agent CLI runtimes. The hosted app needs one to execute agent work. Use a macOS or Linux execution machine and install and authenticate Claude Code or Codex there. Native Windows agent execution is not supported: the daemon has no Windows sandbox. Choose the desktop app for your own computer or the prebuilt CLI for a remote machine; neither path requires building Lykeion from source.

## Connect with the desktop app

1. Open the [download page](/download/) and choose the desktop app for your platform.
2. Install it and sign in to your lab account. The app includes the agent daemon.
3. Check Settings → Connections → Machines for this computer and its detected runtimes. Use its daemon controls to start execution when needed.
4. Confirm that the intended Claude Code or Codex runtime is online and healthy before assigning work.

## Connect a remote computer with the prebuilt CLI

Use the macOS / Linux instructions on the [download page](/download/). Linux also needs bubblewrap; macOS uses its built-in sandbox. Install and sign in to Claude Code or Codex on that machine first.

Run the published installer, sign in to Lykeion, select your workspace and start the daemon:

\`\`\`sh
curl -fsSL https://raw.githubusercontent.com/lykeion-tech/lykeion/main/scripts/install.sh | bash
lyk login --email you@lab.org --server https://app.lykeion.tech
lyk workspace switch <slug>
lyk daemon start
\`\`\`

Replace the email and workspace slug with your own, and enter the emailed code in the terminal. Follow the installer's PATH instructions if lyk is not found. These commands also work over SSH. Use your actual hosted origin if it differs.

## Operate your lab from Claude Code or Codex

Once \`lyk whoami\` shows your Lykeion account, install the [Lykeion CLI skill](https://github.com/lykeion-tech/lykeion-cli) in Claude Code or Codex. It uses your existing CLI sign-in to inspect the board, check pending Decisions, and work with studies and Autoresearch.

## Alternative: build from source

The dashboard's Add a machine dialog also offers a source-build path. This alternative requires Go 1.26 and a Lykeion source checkout. Run its displayed build command from the checkout's server directory, then follow its login and daemon instructions. The dialog also displays Windows build commands, but building or starting a daemon does not supply the missing Windows sandbox. Use a macOS or Linux execution machine for agent runs.

## Confirm execution is ready

[![The paired demonstration machine marked Online](/docs/connected-machine-detail.jpg)](/docs/connected-machine.jpg)

The detail shows the demonstration Mac marked Online. Open the image for the full Machines view.

Open the detected computer and inspect its runtimes. Confirm the intended CLI is online and healthy. The daemon discovers supported CLIs; a computer being online does not guarantee that every provider is authenticated.

## Diagnose connection problems

On macOS or Linux, inspect status and logs:

\`\`\`sh
lyk daemon status
lyk daemon logs
\`\`\`

If no computer appears, check the account, hosted server address and network reachability. An address containing localhost refers to the computer running the command, so it cannot identify a remote hosted server.

Do not paste login codes or provider credentials into studies. Once the desired runtime is usable, return to [your first study](/docs/quickstart/). Use [runs](/docs/runs/) and [troubleshooting](/docs/troubleshooting/) when a connected machine still cannot execute work.
`,
  },
  {
    slug: 'runs',
    title: 'Runs and progress',
    description:
      'Read an execution attempt, distinguish waiting from failure and locate the work it produced.',
    section: 'Machine and execution',
    markdown: `# Runs and progress

A run records an execution attempt by an agent runtime. A study can involve multiple runs and more than one stage. You need access to the study or conversation whose progress you want to inspect.

## Read the latest attempt

1. Open the study and inspect Activity.
2. Expand the relevant agent card to read its progress, tool activity and hand-in where available.
3. Check whether work is waiting, running, ended or reporting an error.
4. Inspect the produced files or notebook records before deciding whether the requested task was delivered.

Read status and transcript together. A quiet transcript is not enough to establish that execution has stopped, and a finished agent card is not enough to establish that the whole study is Done.

## Resolve waiting work

Check the selected machine and its relevant CLI runtime. It must be available, healthy and authenticated for the requested work. Verify the account and access when using shared execution. An agent profile can remain visible while its runtime is offline. See [connect a machine](/docs/connect-machine/).

If the study is waiting for review or scoring, inspect that specific status rather than repeatedly changing its assignee. The demonstration study produced Grounding hand-ins but could not pass its unavailable scoring service.

## Respond to failure

Read the error before retrying. Distinguish provider authentication, network failure, unavailable tools and invalid inputs. Fix the relevant cause and preserve useful partial output. If you need to stop active study work, use its Stop study control and then inspect the resulting state.

When adding a follow-up, keep it attached to the relevant agent card so the discussion remains understandable. Check whether the new message creates execution or remains a comment in that context.

Success means you can explain the attempt's outcome and find its deliverable or blocker. Continue with [evidence and results](/docs/evidence-and-results/) for research review and [troubleshooting](/docs/troubleshooting/) for a short diagnostic sequence.
`,
  },
  {
    slug: 'troubleshooting',
    title: 'Troubleshooting',
    description:
      'Find the concrete blocker when setup, agent execution, connectors or review do not work.',
    section: 'Machine and execution',
    markdown: `# Troubleshooting

Start with the state you can observe. Record the workspace, study identifier, time and exact error. Keep credentials out of screenshots and messages. Avoid creating duplicate studies while you diagnose an existing attempt.

## Work is not starting

1. Confirm the selected account and workspace.
2. Open the study and inspect its assignee and scope. Check a bound machine in the agent configuration; the manual study composer does not select a machine.
3. Check Machines for the relevant runtime's online status and health.
4. On the execution computer, check that the supported agent CLI is installed and authenticated.
5. Read the study's latest activity and any explicit waiting reason.

Use [connect a machine](/docs/connect-machine/) for daemon status and logs. An online computer does not establish that every CLI or provider credential is usable.

## An attempt reports an error

Read the actual error before retrying. Authentication failures require the relevant account or provider setup; network failures require connectivity; invalid inputs require correction of the input. Preserve partial hand-ins and files so you can distinguish a failed step from work already produced. See [runs](/docs/runs/).

## A connector is unavailable

Open Settings and Connectors. Read its enabled state and explanation, then inspect any required credential configuration. Some sources work without a key; others need credentials. Do not guess a key is optional or paste secrets into study text. Ask an administrator when the required controls are unavailable.

## Review is waiting

Inspect Review and the current stage status. An agent may have handed in its work while a required scoring or review service remains unavailable. In the demonstration study, Grounding hand-ins existed but scoring did not complete. This did not verify later stages or justify marking the study Done.

## Material appears missing

Check Activity before assuming output exists. Files and Notebook can be empty if that type of record was never produced. For a loading error, retry the failed surface and retain the error if it continues.

Ask your lab administrator for help with the study identifier and a credential-free description of the observed state. Use [evidence and results](/docs/evidence-and-results/) to verify the recovered output.
`,
  },
  {
    slug: 'coresearch',
    title: 'Coresearch',
    description:
      'Start an ongoing research conversation and inspect its project files, notebook and compute surfaces.',
    section: 'Collaboration',
    markdown: `# Coresearch

Use Coresearch for ongoing research work organized around a project and its conversations. It has its own project files and research surfaces; a study is the separate place to follow stage-scoped work and required review. You need a workspace and a healthy supported runtime on your own account to start execution.

## Start and return to work

1. Open New and choose the research flow that matches your task.
2. Describe the question or material you want to explore. Select an available CLI and model before starting.
3. Open Coresearch to return to projects you started. The list shows the project, CLI, lead and update time.
4. Open the project and choose the conversation you want to continue.

For an existing conversation, execution settings are retained. If a desired runtime is absent from the picker, check your own online, healthy runtimes in [Machines](/docs/connect-machine/).

When you're planning a study, ask Aristotle who could lead it, contribute a method, or review the work. It can consult the research profiles published to your current workspace and explain which experience supports a suggestion. Open the suggested colleague's Overview to read the profile and decide who to involve. See [sharing a research profile](/docs/members-and-roles/).

## Inspect the project surfaces

In a research project, open the experiment surfaces to inspect Notebook, Files and Compute. Use Files to find project material and Notebook to inspect recorded computation. Read the conversation alongside the artifacts it references. Writing projects use their own writing surface.

When editing a file, pay attention to unsaved changes before switching material. Open the referenced file version when inspecting a transcript artifact so you know which content the conversation used.

## Share a chat in Projects

Open the three-dot menu beside an Experiment or Cowrite chat in the Coresearch sidebar and choose **Add to project**. Search for a workspace project or create one from the picker. Selecting a project immediately lets anyone who can view that project read the conversation and download its saved artifacts. They open it from the project's **Coresearches** tab. Existing Cowrite collaborators keep their current permissions.

Each chat appears in one workspace project at a time. Select another project to move it, or choose **Remove from project** to remove that project's access. The conversation, files, and run history stay together. From a project's **Coresearches** tab, the chat owner can pin, rename, archive, or delete a chat. Use **Filter** to switch to **Archived**, where project viewers can still read archived chats and owners can restore them. Under **Display**, select **experiment** or **cowrite** to show that mode. Select the same mode again to show both.

## Keep work understandable

Use a clear project name and separate conversations when the question changes. The Coresearch list supports renaming and archiving projects; review the archive confirmation before applying it. Returning to an existing project preserves its context better than starting duplicate work.

Success means you can reopen the project, continue the intended conversation and locate the material it produced. If no execution starts, inspect the runtime and specific error. If you need a staged research process, create a [study](/docs/studies/); use [Aristotle](/docs/aristotle/) for questions about workspace evidence.
`,
  },
  {
    slug: 'autoresearch',
    title: 'Autoresearch',
    description:
      'Create a repeatable research runbook, choose its trigger and inspect each resulting run.',
    section: 'Collaboration',
    markdown: `# Autoresearch

Autoresearch runs a saved research runbook on a schedule or from a webhook. Use it for recurring tasks such as a literature watch after you have tested the same work manually. You need a workspace, an appropriate assignee and a machine that will be available when the trigger fires.

## Create a repeatable task

1. Open Autoresearch and choose New autoresearch.
2. Enter a clear name and a runbook with the goal, inputs, ordered steps and expected output. The agent reads this runbook on every run.
3. Select the assignee and review its stage scope.
4. Choose Create a study to put each run on the board, or Run only to report back without creating a study.
5. Choose Schedule or Webhook. For a schedule, review the time, recurrence and timezone shown by the product.
6. Create the autoresearch and inspect its Triggers section.

Check that a trigger actually exists. Creation can succeed while trigger creation is refused; the app reports that separately. An entry with no triggers can still be run by hand.

## Test and inspect

Use Run now on an active autoresearch for a controlled first attempt. Inspect Runs, then open the created study or run details. Confirm the output matches the runbook and note any source or execution failure. See [runs](/docs/runs/) and [studies](/docs/studies/).

## Control future work

Use Pause to stop future triggering and Resume when the task is ready again. Read the displayed status and any pause reason. Check execution results after changes rather than assuming the next schedule will succeed.

A webhook URL is shown only when freshly created. Copy it securely if you need it and treat it as a trigger secret; do not put it in a study or public screenshot. Each POST to that URL can start work.

Success means you can identify the saved runbook, its actual trigger and the output of a test run. If execution fails, inspect the [machine](/docs/connect-machine/) and connector requirements before relying on unattended work.
`,
  },
  {
    slug: 'connectors',
    title: 'Connectors',
    description:
      'Inspect scientific data and tool connections, configure required access and diagnose availability.',
    section: 'Data',
    markdown: `# Connectors

Connectors let agents reach configured scientific databases and tools. Availability depends on the source, credentials and workspace configuration. You need access to the workspace; adding or changing a shared connector can require an owner or admin.

## Inspect a connection

1. Open Settings and select Connectors under the workspace.
2. Search the catalogue and open the relevant connector.
3. Read its purpose, enabled state and any reason it is unavailable.
4. Inspect required configuration and credential status before assigning research that depends on it.

OpenAlex is presented separately as a built-in source for papers, preprints and abstracts. Other connectors can have different requirements. A source that works without a key is different from one whose required credential is missing.

## Configure access

Use the connector's configuration dialog for credentials and settings. Follow its validation feedback and save only the intended configuration. Keep secrets in credential fields rather than study descriptions, skill files or screenshots.

For a custom connector, choose Add connector if you have access. Inspect the server and tools you are adding, then use the supported remote URL or command configuration. A custom connector is shared with the workspace, so add only a server your lab trusts.

## Verify with a small task

Ask an appropriately configured agent to perform a narrow source lookup. Inspect the actual response and source reference. A configured connector does not guarantee that a particular paper, dataset or remote server is available. Rate limits and provider failures can still interrupt work.

If it fails, check its enabled state and error, the relevant credential and the execution machine's connectivity. Ask an administrator when configuration controls are unavailable. Record inaccessible sources as a research limitation rather than silently substituting invented evidence.

See [agents](/docs/agents/) for capability selection, [skills](/docs/skills/) for working instructions and [troubleshooting](/docs/troubleshooting/) for diagnosing an unsuccessful lookup.
`,
  },
  {
    slug: 'slack',
    title: 'Slack',
    description:
      'Connect Aristotle to a Slack workspace, link your account and ask questions or draft studies from Slack.',
    section: 'Collaboration',
    markdown: `# Slack

Slack lets members of a Slack workspace talk to Aristotle without opening the dashboard. Each question runs [Ask Aristotle](/docs/aristotle/) on the asker's own connected machine, and the answer arrives in Slack with citations that link back to Lykeion. Only the workspace owner sets it up; members then link their own accounts.

## Set up Slack (owner)

1. Open Settings → Connections → Communication in your Lykeion workspace. Only the workspace owner can connect, manage or disconnect Slack.
2. Copy the Slack app manifest shown on the page with the Copy button.
3. At api.slack.com, choose Create app from manifest, paste the manifest and install the app to your Slack workspace.
4. Back in Lykeion, paste the bot token (it starts with xoxb-) and the app-level token (it starts with xapp-).
5. Choose a drafting agent. It must be visible to the whole workspace, not archived, and assigned to a stage; it is the agent that /study runs.
6. Save.

Lykeion keeps both tokens sealed on the server and never shows them again. A Lykeion workspace has one Slack app. If you host Lykeion yourself, the server needs LYK_SECRET_KEY set before Slack is available.

## Link your account

The bot is Aristotle. The first time you message it, Aristotle replies privately with a link. Open it while signed in to Lykeion and confirm. The link is valid for 15 minutes and works once. The email on your Slack profile must be confirmed and match your Lykeion account's email, or the link is refused.

You can see and remove your link in Settings → Connections → Communication → Your accounts. Removing it means you link again the next time you message Aristotle.

## Ask in a DM or a channel

Send Aristotle a direct message, or @mention it in a channel. Aristotle runs the question on your own connected machine. If none is online it replies "Your machine is offline. Connect it in Lykeion and ask again." Connect one as described in [machine connection](/docs/connect-machine/) and ask again.

In a DM, your messages continue the same conversation until you send /new. In a channel, Aristotle answers in the thread, and each Slack thread is its own conversation. Two people in the same thread each get their own private conversation. Conversations started in Slack also appear in Ask Aristotle in the dashboard. Only questions asked from Slack are answered in Slack.

Answers in a Slack channel are visible to everyone in that channel, including people outside this workspace. Ask sensitive questions in a DM. Citations are links to Lykeion; opening one still requires Lykeion access.

Aristotle does not read Slack files yet. If your message has files it replies "Aristotle can't read Slack files yet; I answered from your text only."

## Commands

- /study <request> drafts a study with the workspace's drafting agent on your machine and sends you the link in a DM when it is ready. Review the draft in Lykeion before you start it; see [studies](/docs/studies/).
- /new starts a new conversation.
- /clear deletes the current DM conversation after you confirm.
- /help lists the commands.

## Disconnect

The owner can disconnect Slack in Settings → Connections → Communication. Questions asked from Slack stop; a study draft already in progress still finishes in Lykeion. Conversations stay in Ask Aristotle. If Slack revokes the app, the page shows Reconnect needed; paste fresh tokens to restore it.

Success means Aristotle answers your DM in Slack, the answer cites records in Lykeion, and the conversation appears in Ask Aristotle. If it does not, check your link, your machine and the tokens in Settings → Connections → Communication.
`,
  },
  {
    slug: 'github',
    title: 'GitHub',
    description:
      'Connect GitHub repositories to your workspace and follow pull requests from your studies.',
    section: 'Collaboration',
    markdown: `# GitHub

Connect a GitHub account or organization to see its pull requests on your [studies](/docs/studies/). The integration is read-only. Lykeion makes no changes on GitHub, and the connection gives agents no repository access. Merging a pull request never changes the study stage. Study progression follows the [scientific stages](/docs/scientific-stages/) and their review rules.

## Connect GitHub

1. As the workspace owner, open Settings → Connections → Code and choose **Connect GitHub**. To install the app on another account, choose **Add account or organization**. If the app is already installed, choose **Connect existing installation**, paste its GitHub settings URL, and select **Authorize installation**. Use \`https://github.com/settings/installations/<id>\` for a personal installation or \`https://github.com/organizations/<login>/settings/installations/<id>\` for an organization.
2. From the desktop app, sign in to Lykeion in your system browser with the same owner account, then use **Continue to GitHub** in the desktop app.
3. For a new installation, select the GitHub account or organization and the repositories to share. Authorize the app on GitHub for either connection method. For a personal installation, authorize as the GitHub account that owns it. For an organization installation, you need admin permission on every repository included in the installation.
4. Setup finishes in the browser, where Lykeion shows the result. If your browser has another workspace open, the success notice names the workspace where you started setup. Use **Open** followed by that workspace's name if the action is available. Settings → Connections → Code shows the connected account and the member who connected it. The desktop app updates this panel automatically.

If the authorizing GitHub account fails the ownership check, Lykeion shows “You don't control this GitHub installation. Ask an owner or admin of the account to connect it.” An organization installation must include at least one repository. If it has none, Lykeion shows “Select at least one repository on GitHub, then connect again.”

Lykeion imports open pull requests, including drafts, from the connected repositories. Settings shows **Importing open pull requests…** while the initial import runs. An interrupted import resumes when the server restarts. If Settings shows **Open pull request import failed.**, a workspace owner can select **Retry import** on the active connection. Pull requests already merged or closed are outside the initial import. When repositories are added to the installation, Lykeion imports their open pull requests as well.

## Link pull requests to a study

Use the study identifier shown in Lykeion, such as \`LYK-12\`, which combines the workspace's study prefix and the study number. With **Auto-link pull requests** enabled, Lykeion matches the ID as a whole token in the branch name or title, ignoring case. In the description, put it immediately after a closing keyword, as in \`Closes LYK-12\` or \`Fixes: LYK-12\`. The accepted keywords are close, closes, closed, fix, fixes, fixed, resolve, resolves and resolved, in any case with an optional colon. A bare mention in the description does not link.

One pull request can name several studies. If an ID names a study in more than one workspace connected to the same installation, link it by hand in the intended workspace. Automatic links follow changes to the references in the pull request. The owner can turn off **Auto-link pull requests** in Settings → Connections → Code to stop new automatic links and keep existing ones.

To link by hand, open a study's details pane, select **Add pull request** (+) under **Pull requests**, paste the GitHub pull request URL and select **Link pull request**. Any workspace member can add or remove a link. For a repository outside the connection, Lykeion shows “That repository isn't connected to this workspace.”

To unlink it, open the row's actions menu and choose **Remove from study**. Lykeion remembers the removal and never automatically links that pull request to that study again. You can still add it by hand.

## Read a pull request row

Each row opens the pull request on GitHub and shows its repository, number, title, author, state, lines added and deleted, and files changed. States are **Open**, **Draft**, **Merged** and **Closed**. Open and draft pull requests can show whether checks passed, failed or are still running, along with mergeability when GitHub reports it. A **stale** label means Lykeion is showing the last available result. Merged and closed rows show the final state without checks or mergeability.

## Disconnect or uninstall

In Settings → Connections → Code, the workspace owner can choose **Disconnect** for an account. The pull requests and links already recorded in that workspace stay visible and stop updating through that connection. The app stays installed on GitHub. A suspended installation appears as **Suspended on GitHub** and resumes updating when unsuspended.

Uninstall the app in GitHub to revoke the installation across every Lykeion workspace that connected it. Removing individual repositories in GitHub stops updates for those repositories.

## Set up the GitHub App on a self-hosted server

Register one GitHub App for your Lykeion server. Set \`PUBLIC_URL\` to the server's public HTTPS URL. For a local development lab, register a separate GitHub App and expose the local server through an HTTPS tunnel. Set \`PUBLIC_URL\` to the tunnel's public URL and use that host for both URLs below.

1. Set **Metadata**, **Pull requests**, **Checks** and **Commit statuses** to read-only under repository permissions.
2. Enable **Request user authorization (OAuth) during installation**. Set the callback URL to \`https://<host>/api/github/setup\`.
3. Activate the webhook, set its URL to \`https://<host>/api/webhooks/github\` and enter a webhook secret. Subscribe to **Pull request**, **Check suite**, **Check run** and **Status**. GitHub Apps receive **Installation** and **Installation repositories** events automatically.
4. If users need to install the app on GitHub accounts or organizations outside the account that owns the app, select **Any account** under **Where can this GitHub App be installed?**

See [Registering a GitHub App](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/registering-a-github-app) and [Webhook events and payloads](https://docs.github.com/en/webhooks/webhook-events-and-payloads#installation) in GitHub's documentation.

Set these six environment variables on the Lykeion server.

- \`GITHUB_APP_SLUG\` is the slug in the GitHub App's install URL.
- \`GITHUB_APP_ID\` is the numeric App ID.
- \`GITHUB_APP_PRIVATE_KEY\` is the contents of the generated PEM private key.
- \`GITHUB_APP_CLIENT_ID\` is the App's OAuth client ID.
- \`GITHUB_APP_CLIENT_SECRET\` is the App's OAuth client secret.
- \`GITHUB_WEBHOOK_SECRET\` is the same secret you entered for the webhook.

Connect requires all six values. If the server is missing any, Settings shows “GitHub isn't configured on this server.” Webhook signature verification uses the webhook secret.
`,
  },
  {
    slug: 'jev',
    title: 'Jev',
    description: 'Understand how Jev and server checks review research items and guide the next step.',
    section: 'Research',
    markdown: `# Jev

Agents carry out the work in a study; Jev helps review research items before configured stages advance. It is an independent check on the work, not a progress meter or a grade for an agent. Open a study's Review surface to inspect the recorded checks, evidence and outstanding limits.

## Your own TypeSafe key

Jev is run by TypeSafe and reviews a study only under its owner's own TypeSafe key. Save yours in **Settings → API Tokens**; it is stored encrypted on the server and sent only to TypeSafe. Without a key, nothing is sent to TypeSafe: the server still runs its checks, and when a stage needs judgment the study asks its owner in [Decisions](/docs/decisions/) to approve the stage, redo it with guidance, close the study or park it.

## Review the work, item by item

Review concerns the items a stage hands in: for example, sources in Grounding, hypotheses, experimental outputs and values, or assessments and verdicts in Validation. The checks depend on the stage and the item. A failed item can be sent back with a specific reason, while items that still stand can be retained. Study completion still depends on its configured scope and required review.

The server performs checks it can verify mechanically, such as resolving a source or checking that a cited record exists. Jev answers focused questions that require judgment using the supplied research context and evidence. Items that fail applicable prerequisite checks are not necessarily sent to Jev. Review uses bounded evidence for its questions; for example, Grounding screens a source without telling Jev who declared it.

## What the stages examine

- **Grounding:** whether sources can be resolved and bear on the research question or its conditions, including checks for potentially missed papers.
- **Hypotheses:** whether predictions are falsifiable and supported by cited evidence, and whether the exact prediction was already tested.
- **Experimentation:** whether recorded outputs and values satisfy the applicable checks; some Jev checks give warnings rather than deciding the stage.
- **Validation:** whether recorded assessments and verdicts are supported, with objections and limitations considered where applicable.

The precise checks and available evidence vary. Inspect the recorded questions and results in Review rather than treating this list as a complete checklist.

## From checks to the next step

Jev's answers contribute to recorded checks; Lykeion applies its review rules to the results. A stage may pass, carry an unclear item forward, or send failed work back for a limited redo. If the redo still fails, the study is blocked. In some cases the study asks its owner for a decision. When the owner has saved a key but TypeSafe cannot answer, the work waits rather than passing.

Review can identify a weakest item where applicable, giving the Team a more specific starting point. Unclear is distinct from a pass or a failure, and a warning does not necessarily decide the stage. Open [Decisions](/docs/decisions/) when a person needs to respond, and use [scientific stages](/docs/scientific-stages/) to follow progress through the study's scope.
`,
  },
];

export const articles: DocArticle[] = [...authoredArticles.filter(article => article.slug !== 'skills'), ...authoredArticles.filter(article => article.slug === 'skills')].map(article => {
  const learning = learningMarkdown(article.slug);
  return learning ? { ...article, proseMarkdown: article.markdown, markdown: `${article.markdown.trimEnd()}\n\n${learning}` } : article;
});

export function getArticle(slug: string): DocArticle | undefined {
  return articles.find((article) => article.slug === slug);
}
