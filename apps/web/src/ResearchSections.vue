<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { ArrowUpRight } from '@lucide/vue';

defineProps<{ ctaHref: string }>();

const activeCase = ref(0);
const autoplayPaused = ref(false);
const playbackPaused = ref(false);
const reducedMotion = ref(false);
const progressKey = ref(0);
let caseTimer: ReturnType<typeof setInterval> | undefined;
let motionQuery: MediaQueryList | undefined;
function selectCase(index: number) {
  activeCase.value = index;
  activeCapture.value = 0;
  progressKey.value++;
  startCaseTimer();
}
function startCaseTimer() {
  clearInterval(caseTimer);
  if (autoplayPaused.value || playbackPaused.value || reducedMotion.value || document.hidden) return;
  caseTimer = setInterval(() => selectCase((activeCase.value + 1) % cases.length), 8000);
}
function setCasePaused(paused: boolean) {
  autoplayPaused.value = paused;
  progressKey.value++;
  startCaseTimer();
}
function leaveCase(event: FocusEvent | MouseEvent) {
  const section = event.currentTarget as HTMLElement;
  if (event instanceof FocusEvent && section.contains(event.relatedTarget as Node | null)) return;
  if (event instanceof MouseEvent && section.contains(document.activeElement)) return;
  setCasePaused(false);
}
function syncMotion() {
  reducedMotion.value = motionQuery?.matches ?? false;
  progressKey.value++;
  startCaseTimer();
}
function togglePlayback() {
  playbackPaused.value = !playbackPaused.value;
  progressKey.value++;
  startCaseTimer();
}
function syncVisibility() {
  progressKey.value++;
  startCaseTimer();
}
onMounted(() => {
  motionQuery = window.matchMedia('(prefers-reduced-motion: reduce)');
  motionQuery.addEventListener('change', syncMotion);
  document.addEventListener('visibilitychange', syncVisibility);
  syncMotion();
});
onUnmounted(() => {
  clearInterval(caseTimer);
  motionQuery?.removeEventListener('change', syncMotion);
  document.removeEventListener('visibilitychange', syncVisibility);
});
const captures = {
  board: {
    src: '/demo/board.webp',
    alt: 'Lykeion Demo study board with the physical activity and sleep assessment in Grounding and empty later stages.',
  },
  study: {
    src: '/demo/study.webp',
    alt: 'Physical activity and sleep study in Lykeion Demo, with its research question, completed researcher reply and saved evidence summary in Files.',
  },
  run: {
    src: '/demo/run.webp',
    alt: 'Expanded Codex researcher run in Lykeion Demo, showing its evidence-assessment transcript, recorded tool-error counts and saved output.',
  },
  evidence: {
    src: '/demo/evidence.webp',
    alt: 'Lykeion Demo grounding report with one validated observational sleep source, source-service limitations and the saved evidence summary.',
  },
  team: {
    src: '/demo/team.webp',
    alt: 'Configured Grounding Team in Lykeion Demo with leader, auditor, searcher and verifier roles; the selected leader uses Codex.',
  },
  experiment: {
    src: '/demo/experiment.webp',
    alt: 'Lykeion Demo Coresearch Experiment with versioned screening-template files and a recorded successful schema check of 30 columns and zero study rows.',
  },
  cowrite: {
    src: '/demo/cowrite.webp',
    alt: 'Lykeion Demo Cowrite showing pending LaTeX edits and the compiled physical activity and sleep review outline with a demonstration disclaimer.',
  },
  autoresearch: {
    src: '/demo/autoresearch.webp',
    alt: 'Paused Sleep evidence watch in Lykeion Demo with its saved runbook, no triggers and no runs.',
  },
  'autoresearch-list': {
    src: '/demo/autoresearch-list.webp',
    alt: 'Lykeion Demo Autoresearch list containing the paused Sleep evidence watch configuration.',
  },
  machines: {
    src: '/demo/machines.webp',
    alt: 'Lykeion Demo Machines settings showing the demo-owned Mac online with two detected runtimes and all idle.',
  },
  aristotle: {
    src: '/demo/aristotle.webp',
    alt: 'Aristotle in Lykeion Demo summarizes the sleep assessment with recorded citations, distinguishes its one validated observational source, and explains the source-service failures and saved output.',
  },
  'aristotle-evidence': {
    src: '/demo/aristotle-evidence.webp',
    alt: 'Aristotle evidence links in Lykeion Demo point to the recorded study, source calls, paper, hand-in and saved assessment output.',
  },
};
type Capture = { src: string; alt: string };
const activeCapture = ref(0);
const enlarged = ref<Capture>(captures.board);
const viewer = ref<HTMLDialogElement>();
const currentCapture = computed(() => cases[activeCase.value].captures[activeCapture.value]);
function enlarge(capture: Capture) {
  enlarged.value = capture;
  viewer.value?.showModal();
}

const cases = [
  {
    title: 'Move a question through the board',
    label: 'STUDY BOARD',
    captures: [captures['board'], captures['study']],
    description:
      'Give a study a question and a Team. Agents work each stage, and the study advances when its checks pass.',
  },
  {
    title: 'Ground every claim in evidence',
    label: 'GROUNDING',
    captures: [captures['evidence']],
    description:
      'Agents survey the literature through Lykeion’s sources server and keep claims connected to recorded sources.',
  },
  {
    title: 'Run the work on your machine',
    label: 'EXPERIMENTATION',
    captures: [captures['run']],
    description:
      'Agent CLIs run inside the operating system’s sandbox, with recorded runs and outputs available to the study.',
  },
  {
    title: 'Put a Team on the problem',
    label: 'TEAMS',
    captures: [captures['team']],
    description: 'Choose the agents that work each column, together with their skills and checks.',
  },
  {
    title: 'Keep the lab’s knowledge close',
    label: 'ARISTOTLE',
    captures: [captures['aristotle'], captures['aristotle-evidence']],
    description:
      'Ask the workspace’s persistent memory about its decisions and knowledge as your research develops.',
  },
  {
    title: 'Work side by side with agents',
    label: 'CORESEARCH',
    captures: [captures['experiment'], captures['cowrite']],
    description:
      'Use shared projects for files, kernels and recorded runs, or write a LaTeX paper together with agents.',
  },
];
const scenarios = [
  {
    tag: 'RESEARCHER',
    icon: '✳',
    title: 'Start with a question.',
    text: 'Create a study, choose an agent or Team, and follow the work as it moves through the board.',
    capture: captures.study,
  },
  {
    tag: 'AGENT',
    icon: '⌘',
    title: 'Work in a real environment.',
    text: 'Agents run the tools they need on a connected machine, with their work and outputs recorded.',
    capture: captures.run,
  },
  {
    tag: 'LAB',
    icon: '◇',
    title: 'Keep evidence in reach.',
    text: 'Sources, runs and decisions stay connected to the study, so the team can return to the work.',
    capture: captures.evidence,
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
const cards = ref([
  {
    icon: '▧',
    title: 'A board for every study',
    text: 'See questions move through Grounding, Hypotheses, Experimentation and Validation.',
    captures: [captures['board'], captures['study']],
    selected: 0,
  },
  {
    icon: '✎',
    title: 'Experiment and write together',
    text: 'Share files, kernels and runs, then work on a LaTeX paper with agents.',
    captures: [captures['experiment'], captures['cowrite']],
    selected: 0,
  },
  {
    icon: '◷',
    title: 'Autoresearch',
    text: 'Configure scheduled or event-triggered work. This demo shows a paused watch with no runs.',
    captures: [captures['autoresearch'], captures['autoresearch-list']],
    selected: 0,
  },
  {
    icon: '⌁',
    title: 'A memory for your lab',
    text: 'Aristotle keeps the workspace’s decisions and knowledge close at hand.',
    captures: [captures['aristotle'], captures['aristotle-evidence']],
    selected: 0,
  },
]);
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
    <dialog ref="viewer" class="capture-dialog" aria-label="Enlarged Lykeion Demo screenshot">
      <div class="dialog-toolbar">
        <strong>Lykeion Demo</strong><button type="button" @click="viewer?.close()">Close ×</button>
      </div>
      <p>{{ enlarged.alt }}</p>
      <div
        class="dialog-image"
        tabindex="0"
        aria-label="Full resolution screenshot; scroll to inspect"
      >
        <img :src="enlarged.src" :alt="enlarged.alt" width="1907" height="870" />
      </div>
      <p>Full resolution capture. Scroll horizontally on smaller screens to read the dashboard.</p>
    </dialog>
    <section id="board" class="research-wrap intro-section">
      <h2>Your question. Your evidence.<br />Your whole lab, working together.</h2>
      <p>Turn a research question into a study that agents can work and people can steer.</p>
    </section>

    <section class="research-wrap use-cases" aria-label="Ways to use Lykeion" @mouseenter="setCasePaused(true)" @mouseleave="leaveCase" @focusin="setCasePaused(true)" @focusout="leaveCase">
      <div class="case-top">
        <span>USE CASES</span><span>{{ activeCase + 1 }} / {{ cases.length }} <button v-if="!reducedMotion" class="playback-toggle" type="button" @click="togglePlayback">{{ playbackPaused ? 'Play' : 'Pause' }}</button></span>
      </div>
      <div class="case-layout">
        <div class="case-visual">
          <Transition name="capture" mode="out-in">
            <button
              :key="currentCapture.src"
              class="capture-button"
              type="button"
              aria-label="Enlarge selected dashboard screenshot"
              @click="enlarge(currentCapture)"
            >
              <img :src="currentCapture.src" :alt="currentCapture.alt" width="1907" height="870" />
            </button>
          </Transition>
          <div class="capture-controls" aria-label="Screenshot views">
            <button
              v-for="(capture, index) in cases[activeCase].captures"
              :key="capture.src"
              type="button"
              :aria-pressed="activeCapture === index"
              @click="activeCapture = index"
            >
              {{ capture.src.split('/').pop()?.replace('.webp', '').replaceAll('-', ' ') }}
            </button>
          </div>
        </div>
        <div class="case-list">
          <article v-for="(item, index) in cases" :key="item.title" class="case-item">
            <div class="case-progress" aria-hidden="true"><span v-if="activeCase === index && !reducedMotion" :key="progressKey" :class="{ paused: autoplayPaused || playbackPaused }" /></div>
            <button
              type="button"
              :aria-expanded="activeCase === index"
              @click="selectCase(index)"
            >
              <span>{{ item.title }}</span
              ><span aria-hidden="true">{{ activeCase === index ? '−' : '+' }}</span>
            </button>
            <div v-if="activeCase === index" class="case-detail">
              <p>{{ item.description }}</p>
              <a :href="ctaHref" class="light-button"
                >Start a study <ArrowUpRight :size="20" aria-hidden="true" /></a
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
          <button
            class="scenario-visual capture-button"
            type="button"
            :aria-label="`Enlarge ${item.tag.toLowerCase()} screenshot`"
            @click="enlarge(item.capture)"
          >
            <img
              :src="item.capture.src"
              :alt="item.capture.alt"
              width="1907"
              height="870"
              loading="lazy"
            />
          </button>
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
        <a :href="ctaHref" class="light-button">Open your lab <ArrowUpRight :size="20" aria-hidden="true" /></a>
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
      <button
        class="board-frame capture-button"
        type="button"
        aria-label="Enlarge Machines screenshot"
        @click="enlarge(captures.machines)"
      >
        <img
          :src="captures.machines.src"
          :alt="captures.machines.alt"
          width="1907"
          height="870"
          loading="lazy"
        />
      </button>
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
          <Transition name="capture" mode="out-in">
            <button
              :key="item.selected"
              class="feature-art capture-button"
              type="button"
              :aria-label="`Enlarge ${item.title} screenshot`"
              @click="enlarge(item.captures[item.selected])"
            >
              <img
                :src="item.captures[item.selected].src"
                :alt="item.captures[item.selected].alt"
                width="1907"
                height="870"
                loading="lazy"
              />
            </button>
          </Transition>
          <div class="capture-controls" :aria-label="`${item.title} views`">
            <button
              v-for="(capture, index) in item.captures"
              :key="capture.src"
              type="button"
              :aria-pressed="item.selected === index"
              @click="item.selected = index"
            >
              {{ capture.src.split('/').pop()?.replace('.webp', '').replaceAll('-', ' ') }}
            </button>
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
          <a :href="ctaHref" class="dark-button">Open your lab <ArrowUpRight :size="20" aria-hidden="true" /></a>
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
        <a href="#board">Explore the board <ArrowUpRight :size="20" aria-hidden="true" /></a>
      </div>
      <div class="resource-small">
        <article>
          <span class="section-label">WORKSPACE</span>
          <h3>Around the board</h3>
          <p>Meet the agents, Teams and tools that make up a Lykeion lab.</p>
          <a href="#platform">Explore the platform <ArrowUpRight :size="20" aria-hidden="true" /></a>
        </article>
        <article>
          <span class="section-label">CORESEARCH</span>
          <h3>Work alongside your agents</h3>
          <p>Experiment with files and runs, or write together in Cowrite.</p>
          <a href="#closer-look">Take a closer look <ArrowUpRight :size="20" aria-hidden="true" /></a>
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
          ><a href="https://github.com/lykeion-ia/lykeion">GitHub</a>
        </div>
      </div>
    </footer>
  </div>
</template>

<style scoped>
.research-page a svg {
  vertical-align: middle;
  margin-left: 6px;
}
.research-page a.light-button svg,
.research-page a.dark-button svg { margin-left: 0; }
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
  gap: 6px;
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

/* Real captures retain the full dashboard; enlargement preserves readable native pixels. */
.case-list {
  display: block;
}
.case-visual {
  min-height: 0;
  padding: 18px;
  display: block;
}
.case-progress { height: 1px; background: #dedfe4; overflow: hidden; }
.case-progress span { display: block; height: 100%; width: 100%; background: #29212f; transform-origin: left; animation: case-progress 8s linear both; }
.case-progress span.paused { animation-play-state: paused; }
.playback-toggle { float: right; border: 0; padding: 0; background: transparent; color: inherit; font: inherit; cursor: pointer; }
.playback-toggle:focus-visible { outline: 2px solid currentColor; outline-offset: 4px; }
@keyframes case-progress { from { transform: scaleX(0); } to { transform: scaleX(1); } }
.capture-button {
  display: block;
  width: 100%;
  padding: 0;
  cursor: zoom-in;
  border: 1px solid #dedfe4;
  border-radius: 9px;
  overflow: hidden;
  background: #fff;
  text-align: left;
  color: #363a43;
  font: inherit;
}
.capture-button img {
  display: block;
  width: 100%;
  height: auto;
  aspect-ratio: 1907 / 870;
}
.capture-controls {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 14px;
}
.capture-controls button,
.dialog-toolbar button {
  border: 1px solid #d7d4de;
  border-radius: 6px;
  padding: 8px 12px;
  background: white;
  color: #46414e;
  font: inherit;
  font-size: 13px;
  cursor: pointer;
  text-transform: capitalize;
}
.capture-controls button[aria-pressed='true'] {
  background: #ede6fc;
  border-color: #8a70b2;
  color: #493669;
}
.capture-button:focus-visible,
.capture-controls button:focus-visible,
.dialog-toolbar button:focus-visible {
  outline: 3px solid #8a70b2;
  outline-offset: 3px;
}
.scenario-visual,
.feature-art {
  height: auto;
}
.board-frame {
  min-height: 0;
  padding: 0;
  background: white;
}
.split-section {
  grid-template-columns: 1fr;
  gap: 36px;
}
.split-copy {
  max-width: 800px;
}
.pillar-list {
  display: grid;
  margin-top: 60px;
  grid-template-columns: repeat(3, 1fr);
  gap: 24px;
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
.feature-card {
  min-height: 0;
}
.capture-enter-active,
.capture-leave-active {
  transition:
    opacity 180ms ease,
    transform 180ms ease;
}
.capture-enter-from,
.capture-leave-to {
  opacity: 0;
  transform: translateY(5px);
}
.capture-dialog {
  width: min(1960px, calc(100vw - 32px));
  max-width: none;
  max-height: calc(100dvh - 32px);
  padding: 16px;
  border: 1px solid #d7d4de;
  border-radius: 12px;
  color: #292c34;
  background: white;
}
.capture-dialog::backdrop {
  background: rgb(12 14 20 / 80%);
}
.dialog-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 20px;
}
.capture-dialog p {
  margin: 12px 0;
  font-size: 14px;
}
.dialog-image {
  overflow: auto;
  max-height: 70dvh;
  border: 1px solid #dedfe4;
}
.dialog-image img {
  display: block;
  width: 1907px;
  max-width: none;
  height: 870px;
}
@media (max-width: 900px) {
  .case-layout {
    grid-template-columns: 1fr;
  }
  .pillar-list {
    grid-template-columns: 1fr;
  }
}
@media (max-width: 600px) {
  .case-list {
    grid-template-columns: 1fr;
  }
  .case-visual {
    padding: 8px;
  }
}
@media (prefers-reduced-motion: reduce) {
  .case-progress span { animation: none; }
  .capture-enter-active,
  .capture-leave-active {
    transition: none;
  }
  .capture-enter-from,
  .capture-leave-to {
    transform: none;
  }
}
</style>
