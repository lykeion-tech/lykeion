<script setup lang="ts">
import { watchEffect } from 'vue';
import { isOfficialMarketingHost, useSession } from './bridge';

const { user, ready, ctaHref, destination } = useSession();

// Signed-in visitors skip the landing page, except on the public marketing
// site. replace() keeps the landing page out of their history.
watchEffect(() => {
  if (user.value && ready.value && !isOfficialMarketingHost(window.location.hostname))
    window.location.replace(destination.value);
});

const nav = [
  { label: 'Board', href: '#board' },
  { label: 'Platform', href: '#platform' },
  { label: 'A closer look', href: '#closer-look' },
  { label: 'Your machine', href: '#machine' },
];

const stages = [
  {
    name: 'Grounding',
    tone: 'grounding',
    text: 'Agents survey the literature through Lykeion’s sources server. Every claim they keep cites a recorded source.',
  },
  {
    name: 'Hypotheses',
    tone: 'hypotheses',
    text: 'From that evidence they state testable hypotheses, and you can pre-register them before any data is touched.',
  },
  {
    name: 'Experimentation',
    tone: 'experimentation',
    text: 'Code, kernels and runs execute in a sandbox on a real machine, with per-study environments and recorded outputs.',
  },
  {
    name: 'Validation',
    tone: 'validation',
    text: 'Results are checked against the plan. A study moves on only when its gate passes, never on an agent’s say-so.',
  },
];

const features = [
  {
    title: 'Teams for every column',
    text: 'Pick the agents that work each stage. Lykeion ships free stage Teams, and labs can upload their own.',
  },
  {
    title: 'Gates, not guesswork',
    text: 'Server checks and Jev scoring decide when a card advances. No routine step waits on a human.',
  },
  {
    title: 'Evidence with provenance',
    text: 'Sources, attachments and run outputs are recorded, so every conclusion traces back to where it came from.',
  },
  {
    title: 'Autoresearch',
    text: 'Schedule agent work or trigger it from events, and come back to studies that moved while you were away.',
  },
  {
    title: 'Coresearch',
    text: 'Shared projects with an Experiment mode for files, kernels and runs, and a Cowrite mode for the paper itself.',
  },
  {
    title: 'Shared lab machines',
    text: 'A lab machine’s runtimes can serve colleagues’ studies, with each person’s runs isolated from the others.',
  },
];

const harnesses = ['Claude Code', 'Codex'];
</script>

<template>
  <header class="nav">
    <div class="wrap nav-inner">
      <a class="brand" href="#top" aria-label="Lykeion home">
        <img src="/lykeion.svg" alt="" width="28" height="28" />
        <span>Lykeion</span>
      </a>
      <nav aria-label="Sections">
        <a v-for="item in nav" :key="item.href" :href="item.href">{{ item.label }}</a>
      </nav>
      <div class="nav-actions">
        <a class="button primary small" :href="ctaHref">{{ user ? 'Dashboard' : 'Get started' }}</a>
      </div>
    </div>
  </header>

  <main id="top">
    <section class="hero wrap">
      <p class="eyebrow">Research, run by agents. Steered by you.</p>
      <h1>The research lab where AI agents do the work</h1>
      <p class="lede">
        Lykeion turns a research question into a study that moves across a board. Agents carry out
        each stage, gates decide when it moves on, and every claim cites a recorded source.
      </p>
      <div class="cta">
        <a class="button primary" :href="ctaHref">Start a study</a>
        <a class="button ghost" href="#board">See how it works</a>
      </div>

      <div class="preview">
        <img
          class="preview-shot"
          src="/lab-board.png"
          alt="A Lykeion lab board with studies in progress"
        />
      </div>
    </section>

    <section id="board" class="wrap section">
      <h2>One board, four stages</h2>
      <p class="section-lede">
        Each study is a card. It moves from column to column only when its gate passes: the server’s
        checks, plus Jev’s score where a stage uses it.
      </p>
      <ol class="stages">
        <li v-for="(stage, i) in stages" :key="stage.name" :class="['stage', stage.tone]">
          <span class="stage-head"
            ><span class="dot" aria-hidden="true" />{{ i + 1 }}. {{ stage.name }}</span
          >
          <p>{{ stage.text }}</p>
        </li>
      </ol>
    </section>

    <section id="platform" class="wrap section">
      <h2>Everything around the board</h2>
      <p class="section-lede">
        The pieces a lab needs to let agents work unattended, and to trust what comes back.
      </p>
      <div class="features">
        <article v-for="f in features" :key="f.title" class="feature">
          <h3>{{ f.title }}</h3>
          <p>{{ f.text }}</p>
        </article>
      </div>
    </section>

    <section id="closer-look" class="wrap section">
      <h2>A closer look</h2>
      <p class="section-lede">What a study looks like while agents are working on it.</p>
      <div class="mockups">
        <figure class="mock">
          <div class="mock-window">
            <div class="mock-bar"><span /><span /><span /></div>
            <div class="mock-body">
              <p class="mock-kicker"><span class="dot experimentation" /> Experimentation</p>
              <p class="mock-title">Does ambient humidity change PLA fatigue life?</p>
              <ul class="checks">
                <li class="ok">Pre-registered hypothesis</li>
                <li class="ok">Environment built: Python 3.12</li>
                <li class="ok">12 of 12 runs recorded</li>
                <li class="wait">Jev score pending</li>
              </ul>
            </div>
          </div>
          <figcaption>Gate checks on a study card</figcaption>
        </figure>

        <figure class="mock">
          <div class="mock-window">
            <div class="mock-bar"><span /><span /><span /></div>
            <div class="mock-body">
              <p class="mock-kicker"><span class="dot grounding" /> Grounding · sources</p>
              <blockquote>
                Fatigue life of printed PLA falls as moisture uptake rises above 0.5% by mass.
              </blockquote>
              <ul class="sources">
                <li><span class="cite">[1]</span> Journal article · recorded</li>
                <li><span class="cite">[2]</span> Dataset · recorded</li>
              </ul>
            </div>
          </div>
          <figcaption>Claims that cite recorded sources</figcaption>
        </figure>

        <figure class="mock">
          <div class="mock-window">
            <div class="mock-bar"><span /><span /><span /></div>
            <div class="mock-body mono">
              <p class="mock-kicker sans"><span class="dot validation" /> Run · sandboxed</p>
              <p><span class="prompt">$</span> python fit_sn_curve.py --rh 30,50,70</p>
              <p class="dim">fitting Basquin model… 3 groups</p>
              <p class="dim">b = -0.087 ± 0.006 (RH 70%)</p>
              <p><span class="prompt">$</span> lyk output put sn-curves.pdf</p>
              <p class="good">saved output sn-curves.pdf</p>
            </div>
          </div>
          <figcaption>Runs and outputs, recorded</figcaption>
        </figure>
      </div>
    </section>

    <section id="machine" class="wrap section split">
      <div>
        <h2>Agents run on your machine</h2>
        <p class="section-lede left">
          A small daemon runs the agent CLIs you already use, inside the operating system’s sandbox,
          with your own sign-in. No single provider is required, and no sandbox means no run.
        </p>
        <ul class="harnesses">
          <li v-for="h in harnesses" :key="h">{{ h }}</li>
        </ul>
      </div>
      <div class="terminal mono" aria-label="Terminal">
        <p><span class="prompt">$</span> lyk login</p>
        <p class="dim">Signed in to Molecular Biophysics Lab</p>
        <p><span class="prompt">$</span> lyk daemon run</p>
        <p class="dim">sandbox: seatbelt · runtimes: claude, codex</p>
        <p class="good">ready: waiting for studies</p>
      </div>
    </section>

    <section class="wrap section final">
      <img src="/lykeion.svg" alt="" width="56" height="56" />
      <h2>Give your next question to the lab</h2>
      <p class="section-lede">Open a workspace, connect a machine, and start a study.</p>
      <div class="cta">
        <a class="button primary" :href="ctaHref">Open your lab</a>
      </div>
    </section>
  </main>

  <footer class="footer">
    <div class="wrap footer-inner">
      <a class="brand" href="#top" aria-label="Lykeion home">
        <img src="/lykeion.svg" alt="" width="22" height="22" />
        <span>Lykeion</span>
      </a>
      <p>© {{ new Date().getFullYear() }} Lykeion</p>
    </div>
  </footer>
</template>
