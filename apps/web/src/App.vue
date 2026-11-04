<script setup lang="ts">
import { onMounted, onUnmounted, ref, watchEffect } from 'vue';
import { isOfficialMarketingHost, paths, useSession } from './bridge';
import HeroTitle from './HeroTitle.vue';
import ResearchSections from './ResearchSections.vue';

const { user, ready, ctaHref, destination } = useSession();

const menuOpen = ref(false);
const githubStars = ref<number | null>(null);
const formattedStars = (count: number) =>
  count >= 1_000_000
    ? `${(count / 1_000_000).toFixed(1).replace(/\.0$/, '')}m`
    : count >= 1_000
      ? `${(count / 1_000).toFixed(1).replace(/\.0$/, '')}k`
      : String(count);
const heroVideo = ref<HTMLVideoElement | null>(null);
const heroReady = ref(false);
let stopHeroVideo: (() => void) | undefined;

onMounted(() => {
  void fetch('https://api.github.com/repos/lykeion-ia/lykeion', {
    headers: { Accept: 'application/vnd.github+json' },
  })
    .then((response) => (response.ok ? response.json() : null))
    .then((data) => {
      if (typeof data?.stargazers_count === 'number') githubStars.value = data.stargazers_count;
    })
    .catch(() => {});
  const video = heroVideo.value;
  if (!video) return;
  const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
  let visible = true;
  const syncPlayback = () => {
    if (reducedMotion.matches || document.hidden || !visible) {
      video.pause();
      return;
    }
    void video.play().catch(() => {
      heroReady.value = false;
    });
  };
  const observer = new IntersectionObserver(([entry]) => {
    visible = entry.isIntersecting;
    syncPlayback();
  });
  observer.observe(video);
  reducedMotion.addEventListener('change', syncPlayback);
  document.addEventListener('visibilitychange', syncPlayback);
  syncPlayback();
  stopHeroVideo = () => {
    observer.disconnect();
    reducedMotion.removeEventListener('change', syncPlayback);
    document.removeEventListener('visibilitychange', syncPlayback);
    video.pause();
  };
});
onUnmounted(() => stopHeroVideo?.());

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
</script>

<template>
  <header class="nav" @keydown.esc="menuOpen = false">
    <a class="nav-logo" href="#top" aria-label="Lykeion home" @click="menuOpen = false">
      <span>Lykeion</span>
    </a>
    <nav class="primary-nav" aria-label="Main navigation">
      <button type="button" disabled>Use cases</button>
      <button type="button" disabled>Docs</button>
      <button type="button" disabled>Changelog</button>
    </nav>
    <div class="nav-external">
      <a
        class="nav-github"
        href="https://github.com/lykeion-ia/lykeion"
        target="_blank"
        rel="noreferrer"
      >
        <img
          class="github-mark"
          src="https://github.githubassets.com/favicons/favicon.svg"
          alt=""
        />
        <span>GitHub</span
        ><span v-if="githubStars !== null" class="github-stars">{{
          formattedStars(githubStars)
        }}</span>
      </a>
    </div>
    <nav class="hero-nav" aria-label="Sections">
      <button
        class="menu-toggle"
        type="button"
        :aria-expanded="menuOpen"
        aria-controls="site-menu"
        @click="menuOpen = !menuOpen"
      >
        Menu
      </button>
      <div v-if="menuOpen" id="site-menu" class="menu-links">
        <button class="mobile-menu-item" type="button" disabled>Use cases</button>
        <button class="mobile-menu-item" type="button" disabled>Docs</button>
        <button class="mobile-menu-item" type="button" disabled>Changelog</button>
        <a class="mobile-menu-github" href="https://github.com/lykeion-ia/lykeion">GitHub ↗</a>
        <a v-for="item in nav" :key="item.href" :href="item.href" @click="menuOpen = false">{{
          item.label
        }}</a>
        <a :href="ctaHref">{{ user ? 'Dashboard' : 'Get started' }}</a>
      </div>
    </nav>
  </header>

  <main id="top">
    <section class="hero" aria-label="About Lykeion">
      <video
        ref="heroVideo"
        class="hero-video"
        :class="{ 'is-ready': heroReady }"
        poster="/hero-frame.webp"
        muted
        loop
        playsinline
        preload="metadata"
        aria-hidden="true"
        @playing="heroReady = true"
        @waiting="heroReady = false"
        @error="heroReady = false"
      >
        <source src="/hero-60fps.webm" type="video/webm" />
        <source src="/hero-60fps.mp4" type="video/mp4" />
      </video>
      <div class="hero-intro">
        <HeroTitle />
        <p class="hero-description">
          Lykeion turns a research question into a study that moves across a board. Agents carry out
          each stage, gates decide when it moves on, and every claim cites a recorded source.
        </p>
        <div class="hero-actions">
          <a class="hero-action hero-action-primary" :href="paths.login">Start free trial</a>
          <button class="hero-action hero-action-secondary" type="button" disabled>
            <svg
              class="download-icon"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.8"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <path d="M12 3v11m-4-4 4 4 4-4M5 17v3h14v-3" />
            </svg>
            Download Desktop
          </button>
        </div>
      </div>
      <div class="hero-caption" aria-hidden="true">
        <span>© 2026 Lykeion</span><span>SCROLL TO EXPLORE ↓</span>
      </div>
    </section>

    <ResearchSections :cta-href="ctaHref" />
  </main>
</template>
