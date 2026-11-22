<script setup lang="ts">
import { computed, ref } from 'vue';
import { version } from '../../../../../package.json';
import Icon from '../../ui/Icon.vue';
import Modal from '../../ui/Modal.vue';
import { releaseHighlights } from './release-highlights';

const emit = defineEmits<{ close: [] }>();
const active = ref(0);
const card = computed(() => releaseHighlights[active.value]);
const last = computed(() => active.value === releaseHighlights.length - 1);
</script>

<template>
  <Modal
    v-slot="{ close }"
    title="What’s new in Lykeion"
    class="release-dialog"
    @close="emit('close')"
  >
    <header class="release-header">
      <div>
        <h2>What’s new in Lykeion</h2>
        <span>Version {{ version }}</span>
      </div>
      <button
        class="icon-button"
        type="button"
        aria-label="Close release highlights"
        @click="close"
      >
        <Icon name="close" :size="18" />
      </button>
    </header>

    <article class="release-card" aria-live="polite">
      <div class="release-illustration" :data-color="card.color" aria-hidden="true">
        <div class="release-flow">
          <span>{{ card.details[0] }}</span>
          <div class="release-feature">
            <Icon :name="card.icon" :size="25" />
            <strong>{{ card.details[1] }}</strong>
          </div>
          <span>{{ card.details[2] }}</span>
        </div>
      </div>
      <div class="release-copy">
        <h3>{{ card.title }}</h3>
        <p>{{ card.description }}</p>
      </div>
    </article>

    <footer class="release-footer">
      <span aria-label="Card progress">{{ active + 1 }} / {{ releaseHighlights.length }}</span>
      <div>
        <button v-if="active" class="button" type="button" @click="active--">Back</button>
        <button class="button primary" type="button" @click="last ? close() : active++">
          {{ last ? 'Done' : 'Next' }}
          <Icon v-if="!last" name="right" :size="14" />
        </button>
      </div>
    </footer>
  </Modal>
</template>

<style scoped>
.release-dialog {
  width: 584px;
  max-width: calc(100vw - 24px);
  max-height: calc(100dvh - 24px);
  margin: auto;
  padding: 22px;
  border-color: var(--border);
  background: var(--surface);
}
.release-header,
.release-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.release-header {
  margin-bottom: 18px;
}
.release-header h2,
.release-copy h3 {
  margin: 0;
  font-weight: 650;
}
.release-header h2 {
  font-size: 20px;
}
.release-header span,
.release-footer > span {
  color: var(--muted);
  font-size: 12px;
}
.release-header .icon-button {
  align-self: flex-start;
}
.release-card {
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--fill-card);
}
.release-illustration {
  --art-color: #3978a9;
  display: grid;
  min-height: 180px;
  place-items: center;
  padding: 20px;
  background-color: color-mix(in srgb, var(--art-color) 13%, var(--surface));
  background-image: radial-gradient(
    color-mix(in srgb, var(--art-color) 26%, transparent) 1px,
    transparent 1px
  );
  background-size: 14px 14px;
}
.release-illustration[data-color='green'] {
  --art-color: #398669;
}
.release-illustration[data-color='violet'] {
  --art-color: #8060a9;
}
.release-flow {
  display: flex;
  align-items: center;
  justify-content: center;
  text-align: center;
}
.release-flow > span,
.release-feature {
  border: 1px solid var(--border);
  background: var(--surface);
  box-shadow: 0 5px 16px #0001;
}
.release-flow > span {
  padding: 8px 11px;
  border-radius: 8px;
  color: var(--muted);
  font-size: 11px;
}
.release-feature {
  z-index: 1;
  display: grid;
  justify-items: center;
  gap: 10px;
  max-width: 176px;
  padding: 19px 17px;
  border-color: color-mix(in srgb, var(--art-color) 45%, var(--border));
  border-radius: 12px;
  font-size: 13px;
}
.release-feature svg {
  color: var(--art-color);
}
.release-copy {
  padding: 19px 20px 22px;
}
.release-copy h3 {
  font-size: 21px;
  line-height: 1.25;
}
.release-copy p {
  min-height: 3em;
  margin: 8px 0 0;
  color: var(--muted);
  line-height: 1.55;
}
.release-footer {
  margin-top: 18px;
}
.release-footer > div {
  display: flex;
  gap: 8px;
}
@media (max-width: 420px) {
  .release-dialog {
    padding: 15px;
  }
  .release-flow > span {
    padding-inline: 5px;
  }
  .release-illustration {
    min-height: 132px;
    padding: 12px;
  }
}
</style>
