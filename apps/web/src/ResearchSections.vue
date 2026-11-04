<script setup lang="ts">
import { ref } from 'vue';

defineProps<{ ctaHref: string }>();

const activeCase = ref(0);
const cases = [
  {
    title: 'Move a question through the board',
    label: 'STUDY BOARD',
    description:
      'Give a study a question and a Team. Agents work each stage, and the study advances when its checks pass.',
    detail: 'Four stages. One shared view.',
    status: 'Experimentation',
    meta: 'Agents working · evidence recorded',
  },
  {
    title: 'Ground every claim in evidence',
    label: 'GROUNDING',
    description:
      'Agents survey the literature through Lykeion’s sources server and keep claims connected to recorded sources.',
    detail: 'A traceable foundation for the next step.',
    status: 'Grounding',
    meta: 'Sources · claims · provenance',
  },
  {
    title: 'Run the work on your machine',
    label: 'EXPERIMENTATION',
    description:
      'Agent CLIs run inside the operating system’s sandbox, with recorded runs and outputs available to the study.',
    detail: 'A real environment for real work.',
    status: 'Running',
    meta: 'Sandboxed · outputs recorded',
  },
  {
    title: 'Put a Team on the problem',
    label: 'TEAMS',
    description: 'Choose the agents that work each column, together with their skills and checks.',
    detail: 'The right roles, working together.',
    status: 'Team assigned',
    meta: 'Grounding · Hypotheses · Validation',
  },
  {
    title: 'Keep the lab’s knowledge close',
    label: 'ARISTOTLE',
    description:
      'Ask the workspace’s persistent memory about its decisions and knowledge as your research develops.',
    detail: 'A memory that stays with the lab.',
    status: 'Workspace memory',
    meta: 'Decisions · knowledge · context',
  },
  {
    title: 'Work side by side with agents',
    label: 'CORESEARCH',
    description:
      'Use shared projects for files, kernels and recorded runs, or write a LaTeX paper together with agents.',
    detail: 'From experiment to paper.',
    status: 'Coresearch',
    meta: 'Experiment · Cowrite',
  },
];
const scenarios = [
  {
    tag: 'RESEARCHER',
    icon: '✳',
    title: 'Start with a question.',
    text: 'Create a study, choose an agent or Team, and follow the work as it moves through the board.',
    visual: 'study',
  },
  {
    tag: 'AGENT',
    icon: '⌘',
    title: 'Work in a real environment.',
    text: 'Agents run the tools they need on a connected machine, with their work and outputs recorded.',
    visual: 'run',
  },
  {
    tag: 'LAB',
    icon: '◇',
    title: 'Keep evidence in reach.',
    text: 'Sources, runs and decisions stay connected to the study, so the team can return to the work.',
    visual: 'evidence',
  },
];
const pillars = [
  {
    icon: '✳',
    title: 'Agents and Teams',
    text: 'Work with one agent or choose a Team with roles for each stage.',
  },
  {
    icon: '⌘',
    title: 'Your machine',
    text: 'Run supported agent CLIs in the operating system’s sandbox, with your own sign-in.',
  },
  {
    icon: '◇',
    title: 'Evidence with provenance',
    text: 'Recorded sources and outputs connect the work to the evidence behind it.',
  },
];
const cards = [
  {
    icon: '▧',
    title: 'A board for every study',
    text: 'See questions move through Grounding, Hypotheses, Experimentation and Validation.',
    visual: 'board',
  },
  {
    icon: '✎',
    title: 'Experiment and write together',
    text: 'Share files, kernels and runs, then work on a LaTeX paper with agents.',
    visual: 'write',
  },
  {
    icon: '◷',
    title: 'Autoresearch',
    text: 'Schedule agent work or trigger it from events, so studies can move while you are away.',
    visual: 'schedule',
  },
  {
    icon: '⌁',
    title: 'A memory for your lab',
    text: 'Aristotle keeps the workspace’s decisions and knowledge close at hand.',
    visual: 'memory',
  },
];
const principles = [
  {
    number: '01',
    title: 'Agents do the work',
    text: 'Research moves forward through agent work, not a queue of routine human approvals.',
  },
  {
    number: '02',
    title: 'Gates decide progress',
    text: 'Server checks and stage review determine when a study is ready to move on.',
  },
  {
    number: '03',
    title: 'Evidence stays connected',
    text: 'Claims cite recorded sources, and runs and outputs remain part of the study.',
  },
];
</script>

<template>
  <div class="research-page">
    <section id="board" class="research-wrap intro-section">
      <h2>Your question. Your evidence.<br />Your whole lab, working together.</h2>
      <p>Turn a research question into a study that agents can work and people can steer.</p>
    </section>

    <section class="research-wrap use-cases" aria-label="Ways to use Lykeion">
      <div class="case-top">
        <span>USE CASES</span><span>{{ activeCase + 1 }} / {{ cases.length }}</span>
      </div>
      <div class="case-layout">
        <div class="case-visual" aria-live="polite">
          <div class="case-ui">
            <div class="case-ui-head">
              <span class="tiny-mark">✳</span><span>{{ cases[activeCase].label }}</span
              ><span class="case-dots">•••</span>
            </div>
            <div class="case-ui-body">
              <span class="pill">{{ cases[activeCase].status }}</span>
              <h3>{{ cases[activeCase].detail }}</h3>
              <p>{{ cases[activeCase].meta }}</p>
              <div class="case-lines"><i /><i /><i /></div>
              <div class="case-proof"><span>◉</span> Recorded in your workspace</div>
            </div>
          </div>
        </div>
        <div class="case-list">
          <article v-for="(item, index) in cases" :key="item.title" class="case-item">
            <button type="button" :aria-expanded="activeCase === index" @click="activeCase = index">
              <span>{{ item.title }}</span
              ><span aria-hidden="true">{{ activeCase === index ? '−' : '+' }}</span>
            </button>
            <div v-if="activeCase === index" class="case-detail">
              <p>{{ item.description }}</p>
              <a :href="ctaHref" class="light-button"
                >Start a study <span aria-hidden="true">↗</span></a
              >
            </div>
          </article>
        </div>
      </div>
    </section>

    <section id="platform" class="research-wrap research-section">
      <div class="center-heading">
        <h2>People and agents work together.<br />On the same research.</h2>
        <p>Different roles, one shared view of the work.</p>
      </div>
      <div class="scenario-grid">
        <article v-for="item in scenarios" :key="item.tag" class="scenario">
          <div class="section-label">
            <span class="icon-tile">{{ item.icon }}</span
            >{{ item.tag }}
          </div>
          <div class="scenario-visual" :class="`visual-${item.visual}`" aria-hidden="true">
            <div v-if="item.visual === 'study'" class="mini-study">
              <span class="mini-bar">STUDY &nbsp; · &nbsp; EXPERIMENTATION</span
              ><strong>Does the evidence support the hypothesis?</strong>
              <div class="mini-stage"><i /> Grounding <b>Complete</b></div>
              <div class="mini-stage"><i /> Hypotheses <b>Complete</b></div>
              <div class="mini-stage"><i /> Experimentation <b>Working</b></div>
            </div>
            <div v-else-if="item.visual === 'run'" class="mini-terminal">
              <span>RUN &nbsp; · &nbsp; SANDBOXED</span>
              <p>$ lyk daemon run</p>
              <p class="muted">sandbox ready<br />waiting for studies</p>
              <p class="mint">● Agent working</p>
            </div>
            <div v-else class="mini-evidence">
              <span>RECORDED SOURCES</span>
              <div>01 &nbsp; Journal article <b>↗</b></div>
              <div>02 &nbsp; Dataset <b>↗</b></div>
              <div>03 &nbsp; Run output <b>↗</b></div>
            </div>
          </div>
          <h3>{{ item.title }}</h3>
          <p>{{ item.text }}</p>
        </article>
      </div>
    </section>

    <section id="machine" class="research-wrap research-section split-section">
      <div class="split-copy">
        <h2>Agents that work<br />where you do.</h2>
        <p>
          Your machine runs the agent CLIs you already use. Lykeion keeps their work connected to
          the study.
        </p>
        <a :href="ctaHref" class="light-button">Open your lab <span aria-hidden="true">↗</span></a>
        <div class="pillar-list">
          <div v-for="item in pillars" :key="item.title" class="pillar">
            <span class="icon-tile">{{ item.icon }}</span>
            <div>
              <h3>{{ item.title }}</h3>
              <p>{{ item.text }}</p>
            </div>
          </div>
        </div>
      </div>
      <div class="board-frame"><img src="/lab-board.png" alt="A Lykeion study board" /></div>
    </section>

    <section id="closer-look" class="research-wrap research-section">
      <div class="center-heading">
        <h2>A workspace built for<br />the whole research process.</h2>
        <p>The tools around the board, together in one place.</p>
      </div>
      <div class="feature-grid">
        <article v-for="item in cards" :key="item.title" class="feature-card">
          <div class="feature-heading">
            <span>{{ item.icon }}</span>
            <h3>{{ item.title }}</h3>
          </div>
          <p>{{ item.text }}</p>
          <div class="feature-art" :class="`art-${item.visual}`" aria-hidden="true">
            <template v-if="item.visual === 'board'"
              ><div class="board-columns">
                <span>Grounding<i /></span><span>Hypotheses<i /></span><span>Experiment<i /></span
                ><span>Validation<i /></span></div
            ></template>
            <template v-else-if="item.visual === 'write'"
              ><div class="paper">
                <small>CORESEARCH / COWRITE</small><strong>Research, written together.</strong
                ><i /><i /><i />
              </div>
              <span class="paper-mark">Aa</span></template
            >
            <template v-else-if="item.visual === 'schedule'"
              ><div class="calendar">
                <span>MON</span><span>TUE</span><span>WED</span><span>THU</span><span>FRI</span
                ><i /><i /><i /><i /><i /></div
            ></template>
            <template v-else
              ><div class="memory-card">
                <span>✳ &nbsp; ARISTOTLE</span>
                <p>What have we learned so far?</p>
                <div>Knowledge and decisions,<br />ready when you need them.</div>
              </div></template
            >
          </div>
        </article>
      </div>
    </section>

    <section class="research-wrap research-section principles-section">
      <div class="center-heading">
        <h2>Built for research<br />you can follow.</h2>
        <p>From the first question to the evidence behind the result.</p>
      </div>
      <div class="principle-grid">
        <article v-for="item in principles" :key="item.number">
          <span>{{ item.number }}</span>
          <h3>{{ item.title }}</h3>
          <p>{{ item.text }}</p>
        </article>
      </div>
    </section>

    <section class="research-wrap research-section stack-section">
      <div class="section-label">YOUR LAB, YOUR TOOLS</div>
      <h2>Built around the way<br />your lab works.</h2>
      <p>Connect a machine and work with the agent CLIs you already use.</p>
      <div class="stack-grid">
        <div><strong>4</strong><span>STUDY STAGES</span></div>
        <div><strong>1</strong><span>SHARED BOARD</span></div>
        <div><strong>2</strong><span>CORESEARCH MODES</span></div>
        <div><strong>∞</strong><span>QUESTIONS TO ASK</span></div>
      </div>
      <div class="tool-row" aria-label="Lykeion capabilities">
        <span
          v-for="tool in [
            'CLAUDE CODE',
            'CODEX',
            'TEAMS',
            'AUTORESEARCH',
            'ARISTOTLE',
            'EXPERIMENT',
            'COWRITE',
          ]"
          :key="tool"
          >{{ tool }}</span
        >
      </div>
    </section>

    <section class="research-wrap research-section">
      <div class="closing-card">
        <div class="closing-copy">
          <span class="section-label">GET STARTED</span>
          <div>
            <h2>Give your next question<br />to the lab.</h2>
            <p>Open a workspace, connect a machine, and start a study.</p>
          </div>
          <a :href="ctaHref" class="dark-button">Open your lab <span aria-hidden="true">↗</span></a>
        </div>
        <div class="closing-art" role="img" aria-label="An illustrated ancient Greek forum" />
      </div>
    </section>

    <section class="research-wrap research-section resource-section">
      <div class="resource-large">
        <span class="section-label">THE BOARD</span>
        <div class="resource-art">Research, in motion.</div>
        <h3>From a question to a study</h3>
        <p>Follow the work across the board, with its evidence and outputs close at hand.</p>
        <a href="#board">Explore the board ↗</a>
      </div>
      <div class="resource-small">
        <article>
          <span class="section-label">WORKSPACE</span>
          <h3>Around the board</h3>
          <p>Meet the agents, Teams and tools that make up a Lykeion lab.</p>
          <a href="#platform">Explore the platform ↗</a>
        </article>
        <article>
          <span class="section-label">CORESEARCH</span>
          <h3>Work alongside your agents</h3>
          <p>Experiment with files and runs, or write together in Cowrite.</p>
          <a href="#closer-look">Take a closer look ↗</a>
        </article>
      </div>
    </section>

    <footer class="research-footer">
      <div class="research-wrap footer-grid">
        <div>
          <a href="#top" class="footer-brand">Lykeion</a>
          <p>A research lab where agents work<br />and people steer.</p>
        </div>
        <div>
          <span>EXPLORE</span><a href="#board">The board</a><a href="#platform">The platform</a
          ><a href="#closer-look">A closer look</a><a href="#machine">Your machine</a>
        </div>
        <div>
          <span>WORKSPACE</span><a :href="ctaHref">Open your lab</a
          ><a href="https://github.com/lykeion-ia/lykeion">GitHub ↗</a>
        </div>
      </div>
    </footer>
  </div>
</template>

<style scoped>
.research-page {
  background: #090909;
  color: #f2f0eb;
  overflow: hidden;
}
.research-wrap {
  width: min(1230px, calc(100% - 48px));
  margin-inline: auto;
}
.research-page h2,
.research-page h3,
.research-page p {
  margin: 0;
}
.research-page h2 {
  font-size: clamp(30px, 3vw, 44px);
  font-weight: 400;
  line-height: 1.12;
  letter-spacing: -0.025em;
}
.research-page h3 {
  font-size: 17px;
  line-height: 1.35;
  font-weight: 600;
}
.research-page p {
  color: #999;
  line-height: 1.5;
  font-size: 16px;
}
.research-section {
  padding-top: 160px;
}
.intro-section {
  padding-top: 130px;
  padding-bottom: 100px;
}
.intro-section p {
  max-width: 450px;
  margin-top: 24px;
  font-size: 19px;
}
.case-top {
  display: grid;
  grid-template-columns: 1.2fr 1fr;
  gap: 40px;
  font-size: 12px;
  letter-spacing: 0.12em;
}
.case-top span {
  padding-bottom: 16px;
  border-bottom: 1px solid #282828;
}
.case-layout {
  display: grid;
  grid-template-columns: 1.2fr 1fr;
  gap: 40px;
  margin-top: 24px;
  align-items: start;
}
.case-visual {
  min-height: 620px;
  display: grid;
  place-items: center;
  border: 1px solid #292929;
  border-radius: 12px;
  background: linear-gradient(150deg, #161616, #101010);
  padding: 9%;
  overflow: hidden;
}
.case-ui {
  width: 100%;
  max-width: 500px;
  border: 1px solid #333;
  border-radius: 9px;
  background: #0b0b0b;
  box-shadow: 0 30px 70px #0008;
}
.case-ui-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 16px;
  border-bottom: 1px solid #282828;
  font-size: 11px;
  letter-spacing: 0.1em;
  color: #a8a8a8;
}
.tiny-mark,
.icon-tile {
  display: inline-grid;
  place-items: center;
  flex: none;
  background: #b7a4f7;
  color: #15121d;
  border-radius: 5px;
  width: 28px;
  height: 28px;
  font-size: 16px;
}
.case-dots {
  margin-left: auto;
}
.case-ui-body {
  min-height: 340px;
  padding: 36px;
}
.pill {
  display: inline-block;
  padding: 5px 11px;
  border-radius: 30px;
  background: #d8cbff;
  color: #40316e;
  font-size: 12px;
}
.case-ui-body h3 {
  margin-top: 26px;
  font-size: 25px;
  font-weight: 400;
}
.case-ui-body p {
  margin-top: 12px;
  font-size: 14px;
}
.case-lines {
  margin-top: 38px;
}
.case-lines i,
.paper i {
  display: block;
  height: 7px;
  margin-top: 12px;
  background: #292929;
  border-radius: 3px;
}
.case-lines i:nth-child(2) {
  width: 80%;
}
.case-lines i:nth-child(3) {
  width: 55%;
}
.case-proof {
  border-top: 1px solid #282828;
  margin-top: 38px;
  padding-top: 17px;
  color: #929292;
  font-size: 12px;
}
.case-proof span {
  color: #b7a4f7;
  margin-right: 7px;
}
.case-item {
  border-bottom: 1px solid #282828;
}
.case-item button {
  width: 100%;
  padding: 24px 0;
  display: flex;
  justify-content: space-between;
  gap: 18px;
  text-align: left;
  background: none;
  border: 0;
  color: #dedbd5;
  font: 600 16px/1.35 inherit;
  font-family: inherit;
  font-size: 16px;
  cursor: pointer;
}
.case-item button[aria-expanded='false'] {
  color: #969696;
}
.case-item button:hover {
  color: #fff;
}
.case-item button:focus-visible {
  outline: 2px solid #b7a4f7;
  outline-offset: 4px;
}
.case-detail {
  padding: 0 0 28px;
}
.case-detail p {
  max-width: 480px;
}
.light-button,
.dark-button {
  display: inline-flex;
  align-items: center;
  gap: 22px;
  margin-top: 24px;
  padding: 11px 17px;
  border-radius: 30px;
  background: #f6f5f1;
  color: #161616;
  font-size: 14px;
  font-weight: 600;
}
.light-button:hover {
  background: #ded7ef;
}
.center-heading {
  text-align: center;
}
.center-heading p {
  margin-top: 22px;
  font-size: 20px;
}
.scenario-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 24px;
  margin-top: 80px;
}
.section-label {
  font-size: 12px;
  letter-spacing: 0.1em;
  color: #ddd8d0;
}
.scenario .section-label {
  display: flex;
  align-items: center;
  gap: 10px;
}
.scenario-visual {
  height: 290px;
  margin: 24px 0;
  display: grid;
  place-items: center;
  overflow: hidden;
  border: 1px solid #292929;
  border-radius: 12px;
  background: #141414;
}
.scenario h3 {
  margin-bottom: 12px;
}
.scenario p {
  font-size: 15px;
}
.mini-study,
.mini-terminal,
.mini-evidence {
  width: 80%;
  padding: 20px;
  border: 1px solid #303030;
  border-radius: 7px;
  background: #0a0a0a;
  box-shadow: 0 20px 45px #000a;
  font-size: 12px;
  color: #b7b1bd;
}
.mini-study strong {
  display: block;
  color: #eee;
  font-size: 15px;
  line-height: 1.3;
  margin: 20px 0;
}
.mini-bar,
.mini-terminal > span,
.mini-evidence > span {
  font-size: 10px;
  color: #a998e1;
  letter-spacing: 0.08em;
}
.mini-stage,
.mini-evidence div {
  display: flex;
  gap: 8px;
  align-items: center;
  border-top: 1px solid #292929;
  padding: 11px 0;
}
.mini-stage i {
  width: 7px;
  height: 7px;
  background: #aa98e6;
  border-radius: 50%;
}
.mini-stage b,
.mini-evidence b {
  margin-left: auto;
  color: #8a8a8a;
  font-weight: 400;
}
.mini-terminal p {
  margin-top: 20px;
  font-size: 12px;
}
.mini-terminal .mint {
  color: #8bb99e;
}
.mini-evidence div:first-of-type {
  margin-top: 20px;
}
.split-section {
  display: grid;
  grid-template-columns: 1fr 1.05fr;
  gap: 70px;
  align-items: center;
}
.split-copy > p {
  max-width: 490px;
  font-size: 20px;
  margin-top: 24px;
}
.pillar-list {
  display: grid;
  gap: 36px;
  margin-top: 60px;
}
.pillar {
  display: flex;
  align-items: start;
  gap: 14px;
}
.pillar h3 {
  margin-bottom: 7px;
}
.pillar p {
  max-width: 400px;
  font-size: 15px;
}
.board-frame {
  min-height: 640px;
  display: grid;
  place-items: center;
  border-radius: 12px;
  padding: 7%;
  background:
    linear-gradient(#1b2631b5, #1b2631b5),
    url('/hero-frame.webp') center/cover;
}
.board-frame img {
  display: block;
  width: 100%;
  border-radius: 7px;
  box-shadow: 0 20px 50px #0009;
}
.feature-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 24px;
  margin-top: 80px;
}
.feature-card {
  min-height: 430px;
  overflow: hidden;
  border: 1px solid #2a2a2a;
  border-radius: 12px;
  background: #141414;
  padding: 26px;
}
.feature-heading {
  display: flex;
  gap: 12px;
  align-items: center;
}
.feature-heading > span {
  color: #b9a6fb;
  font-size: 24px;
}
.feature-card > p {
  max-width: 530px;
  margin-top: 14px;
  font-size: 15px;
}
.feature-art {
  position: relative;
  display: grid;
  place-items: center;
  height: 260px;
  margin-top: 22px;
  overflow: hidden;
}
.board-columns {
  display: flex;
  gap: 7px;
  width: 95%;
  height: 170px;
  padding: 10px;
  border: 1px solid #333;
  border-radius: 8px;
  background: #0a0a0a;
}
.board-columns span {
  flex: 1;
  padding: 10px 6px;
  border-radius: 5px;
  background: #23222a;
  color: #bcb6cc;
  font-size: 10px;
}
.board-columns span:nth-child(2) {
  background: #292331;
}
.board-columns span:nth-child(3) {
  background: #2c2920;
}
.board-columns span:nth-child(4) {
  background: #202b26;
}
.board-columns i {
  display: block;
  height: 50px;
  margin-top: 12px;
  border-radius: 4px;
  background: #ffffff12;
}
.paper {
  width: 55%;
  min-height: 190px;
  padding: 22px;
  background: #e5dfd3;
  color: #23252b;
  transform: rotate(-5deg);
  box-shadow: 0 20px 30px #0008;
}
.paper small {
  font-size: 9px;
  letter-spacing: 0.1em;
}
.paper strong {
  display: block;
  margin-top: 15px;
  font-size: 20px;
  line-height: 1.1;
  font-weight: 400;
}
.paper i {
  background: #b8b2a9;
  height: 3px;
}
.paper-mark {
  font-size: 85px;
  color: #c8b6fb;
  margin-left: 24px;
}
.calendar {
  width: 80%;
  display: grid;
  grid-template-columns: repeat(5, 1fr);
  gap: 6px;
  padding: 20px;
  background: #0b0b0b;
  border: 1px solid #333;
  border-radius: 9px;
}
.calendar span {
  font-size: 10px;
  color: #aaa;
}
.calendar i {
  height: 65px;
  background: #24232a;
  border-radius: 4px;
}
.calendar i:nth-of-type(2),
.calendar i:nth-of-type(4) {
  background: #61557d;
}
.memory-card {
  width: 75%;
  padding: 25px;
  border: 1px solid #343139;
  border-radius: 10px;
  background: #0b0b0b;
}
.memory-card span {
  color: #b7a4f7;
  font-size: 11px;
}
.memory-card p {
  margin: 22px 0;
  color: #eee;
}
.memory-card div {
  border-radius: 6px;
  padding: 15px;
  background: #242029;
  color: #b5aebd;
  font-size: 14px;
}
.principle-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 24px;
  margin-top: 70px;
}
.principle-grid article {
  min-height: 230px;
  padding: 26px;
  border: 1px solid #2b2b2b;
  border-radius: 10px;
  background: #121212;
}
.principle-grid article > span {
  color: #b6a2ed;
  font-size: 12px;
}
.principle-grid h3 {
  margin-top: 38px;
}
.principle-grid p {
  margin-top: 12px;
  font-size: 15px;
}
.stack-section > p {
  margin-top: 22px;
  font-size: 20px;
}
.stack-section h2 {
  margin-top: 24px;
}
.stack-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 20px;
  margin-top: 70px;
}
.stack-grid div {
  border-left: 1px solid #333;
  padding-left: 20px;
}
.stack-grid strong {
  display: block;
  font-weight: 400;
  font-size: 56px;
  line-height: 1;
}
.stack-grid span {
  display: block;
  color: #999;
  font-size: 11px;
  letter-spacing: 0.1em;
  margin-top: 15px;
}
.tool-row {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
  margin-top: 70px;
}
.tool-row span {
  display: grid;
  place-items: center;
  min-height: 86px;
  flex: 1 1 135px;
  padding: 14px;
  border: 1px solid #292929;
  border-radius: 10px;
  font-size: 11px;
  letter-spacing: 0.08em;
  color: #aaa;
}
.closing-card {
  min-height: 410px;
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 20px;
  padding: 12px;
  background: #f2f0eb;
  border-radius: 12px;
  color: #151515;
}
.closing-copy {
  padding: 22px;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  justify-content: space-between;
  gap: 38px;
}
.closing-copy .section-label {
  color: #333;
}
.closing-copy p {
  margin-top: 18px;
  color: #686868;
}
.dark-button {
  background: #141414;
  color: #fff;
  margin: 0;
}
.dark-button:hover {
  background: #39313c;
}
.closing-art {
  min-height: 385px;
  border-radius: 7px;
  background: url('/hero-frame.webp') center/cover;
}
.resource-section {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 24px;
}
.resource-large,
.resource-small article {
  padding: 14px;
  border: 1px solid #2b2b2b;
  border-radius: 12px;
}
.resource-art {
  min-height: 270px;
  display: grid;
  place-items: center;
  margin: 14px 0;
  background: #171717;
  color: #eee8dc;
  font-size: clamp(30px, 3vw, 48px);
  text-align: center;
}
.resource-section h3 {
  margin-top: 14px;
}
.resource-section p {
  margin-top: 8px;
  font-size: 15px;
}
.resource-section a {
  display: inline-block;
  margin-top: 18px;
  font-size: 14px;
  color: #c0aef4;
}
.resource-small {
  display: grid;
  gap: 24px;
}
.resource-small article {
  padding: 25px;
}
.research-footer {
  margin-top: 160px;
  padding: 75px 0 30px;
  border-top: 1px solid #292929;
}
.footer-grid {
  display: grid;
  grid-template-columns: 2fr 1fr 1fr;
  gap: 40px;
}
.footer-brand {
  font-size: 32px;
}
.footer-grid p {
  margin-top: 12px;
  font-size: 15px;
}
.footer-grid > div:not(:first-child) {
  display: flex;
  flex-direction: column;
  gap: 12px;
  font-size: 14px;
}
.footer-grid > div > span {
  color: #8d8d8d;
  font-size: 11px;
  letter-spacing: 0.1em;
  margin-bottom: 8px;
}
.footer-grid a:hover,
.footer-bottom a:hover,
.resource-section a:hover {
  text-decoration: underline;
}
.footer-bottom {
  display: flex;
  justify-content: space-between;
  gap: 20px;
  padding-top: 80px;
  color: #888;
  font-size: 11px;
  letter-spacing: 0.08em;
}
@media (max-width: 900px) {
  .case-layout,
  .split-section {
    grid-template-columns: 1fr;
  }
  .case-visual {
    min-height: 450px;
  }
  .scenario-grid {
    grid-template-columns: 1fr 1fr;
  }
  .board-frame {
    min-height: 450px;
  }
  .research-section {
    padding-top: 110px;
  }
}
@media (max-width: 600px) {
  .research-wrap {
    width: calc(100% - 36px);
  }
  .intro-section {
    padding-top: 90px;
    padding-bottom: 70px;
  }
  .case-top,
  .case-layout {
    gap: 16px;
  }
  .case-top {
    grid-template-columns: 1fr auto;
  }
  .case-visual {
    min-height: 360px;
    padding: 7%;
  }
  .case-ui-body {
    padding: 22px;
    min-height: 260px;
  }
  .case-ui-body h3 {
    font-size: 21px;
  }
  .scenario-grid,
  .feature-grid,
  .principle-grid,
  .resource-section,
  .closing-card {
    grid-template-columns: 1fr;
  }
  .scenario-grid,
  .feature-grid {
    margin-top: 50px;
  }
  .scenario-visual {
    height: 260px;
  }
  .feature-card {
    min-height: 370px;
  }
  .feature-art {
    height: 220px;
  }
  .stack-grid {
    grid-template-columns: 1fr 1fr;
    row-gap: 36px;
  }
  .closing-card {
    gap: 0;
  }
  .closing-copy {
    min-height: 300px;
  }
  .closing-art {
    min-height: 260px;
  }
  .footer-grid {
    grid-template-columns: 1fr 1fr;
  }
  .footer-grid > div:first-child {
    grid-column: 1/-1;
  }
  .research-footer {
    margin-top: 110px;
  }
  .research-section {
    padding-top: 100px;
  }
}
/* Light palette for the content below the hero. */
.research-page {
  background: #fff;
  color: #1d2026;
}
.research-page p {
  color: #626773;
}
.case-top span,
.case-item,
.research-footer {
  border-color: #e2e4e8;
}
.case-visual,
.scenario-visual,
.feature-card,
.principle-grid article {
  background: #f7f7f8;
  border-color: #e2e4e8;
}
.case-ui,
.mini-study,
.mini-terminal,
.mini-evidence,
.board-columns,
.calendar,
.memory-card {
  background: #fff;
  border-color: #dedfe4;
  color: #5b6070;
  box-shadow: 0 18px 45px rgb(31 38 52 / 9%);
}
.case-ui-head,
.case-proof,
.mini-stage,
.mini-evidence div {
  border-color: #e8e9ed;
  color: #777b86;
}
.case-ui-head,
.mini-bar,
.mini-terminal > span,
.mini-evidence > span {
  color: #75669c;
}
.case-lines i {
  background: #e4e5ea;
}
.case-item button,
.case-item button[aria-expanded='false'] {
  color: #363a43;
}
.case-item button:hover {
  color: #15171b;
}
.light-button {
  background: #20222a;
  color: #fff;
}
.light-button:hover {
  background: #51466b;
}
.section-label {
  color: #555a64;
}
.mini-study strong,
.memory-card p {
  color: #292c34;
}
.mini-stage b,
.mini-evidence b {
  color: #747986;
}
.mini-terminal .mint {
  color: #357c57;
}
.board-columns span {
  background: #edebf1;
  color: #595365;
}
.board-columns span:nth-child(2) {
  background: #eee9f4;
}
.board-columns span:nth-child(3) {
  background: #f2efe5;
}
.board-columns span:nth-child(4) {
  background: #e7f0eb;
}
.board-columns i {
  background: rgb(255 255 255 / 75%);
}
.calendar span {
  color: #666b75;
}
.calendar i {
  background: #edeef1;
}
.calendar i:nth-of-type(2),
.calendar i:nth-of-type(4) {
  background: #cfc4e8;
}
.memory-card span {
  color: #76629f;
}
.memory-card div {
  background: #f0edf4;
  color: #5e5966;
}
.principle-grid article > span {
  color: #76629f;
}
.stack-grid div {
  border-color: #e0e1e5;
}
.stack-grid span,
.footer-grid > div > span,
.footer-bottom {
  color: #777b84;
}
.tool-row span {
  background: #fff;
  border-color: #e2e4e8;
  color: #555a64;
}
.closing-card {
  background: #f2f1ee;
}
.resource-large,
.resource-small article {
  border-color: #e2e4e8;
}
.resource-art {
  background: #f4f3f5;
  color: #29252f;
}
.resource-section a {
  color: #67518d;
}
</style>
