<script setup lang="ts">
import { stages } from '../../../../web/src/app/docs/learning-content';

const examples = [
  [
    'Sleep and memory: find the evidence',
    'Map research on urban heat',
    'Review evidence on air quality',
  ],
  ['Compare explanations for memory retention', 'Explore causes of heat exposure'],
  [
    'Run a sampling simulation',
    'Analyze temperature measurements',
    'Compare two analysis methods',
    'Reproduce a published computation',
  ],
  [
    'Review a memory-retention claim',
    'Check a temperature analysis',
    'Examine a reproduced result',
  ],
];
</script>

<template>
  <section class="stage-board" aria-label="Study stage walkthrough">
    <div
      class="stage-board-scroll"
      tabindex="0"
      role="region"
      aria-label="Scientific stage columns"
    >
      <div class="stage-board-columns">
        <div
          v-for="(stage, index) in stages"
          :key="stage.name"
          class="stage-board-column"
          :class="`stage-board-column-${index}`"
        >
          <div class="stage-board-select">
            <span class="stage-board-heading"
              ><span class="stage-board-dot" aria-hidden="true" /><strong>{{ stage.name }}</strong
              ><span>{{ examples[index]!.length }}</span
              ><span class="stage-board-plus" aria-hidden="true">+</span></span
            >
            <span class="stage-board-cards">
              <span
                v-for="(title, cardIndex) in examples[index]"
                :key="title"
                class="study-example-card"
                ><span
                  >EXAMPLE-{{
                    examples.slice(0, index).reduce((sum, cards) => sum + cards.length, 0) +
                    cardIndex +
                    1
                  }}</span
                ><strong>{{ title }}</strong></span
              >
            </span>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.stage-board {
  color: #20262c;
}
.stage-board-scroll {
  overflow-x: auto;
  border-radius: 12px;
  scrollbar-color: #aab7c3 transparent;
  scrollbar-width: thin;
}
.stage-board-scroll:focus-visible {
  outline: 3px solid #22538b;
  outline-offset: 3px;
}
.stage-board-columns {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 16px;
  min-width: 680px;
}
.stage-board-column {
  border-radius: 12px;
  background: #ecedf0;
  min-height: 480px;
}
.stage-board-column-1 {
  background: #f7eeff;
}
.stage-board-column-2 {
  background: #fbf6e9;
}
.stage-board-column-3 {
  background: #ebf4ec;
}
.stage-board-select {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-height: 480px;
  padding: 0;
  border: 0;
  border-radius: 12px;
  background: transparent;
  color: inherit;
  text-align: left;
  font: 13px var(--sans, sans-serif);
}
.stage-board-heading {
  display: flex;
  align-items: center;
  gap: 4px;
  width: 100%;
  padding: 12px 8px;
  line-height: 18px;
}
.stage-board-heading strong {
  font-weight: 600;
  font-size: 12px;
}
.stage-board-plus {
  margin-left: auto;
  flex: none;
  color: #626c74;
  font-size: 18px;
  line-height: 1;
}
.stage-board-dot {
  width: 12px;
  height: 12px;
  border: 1px solid currentColor;
  border-radius: 50%;
  color: #9b9fa3;
  flex: none;
}
.stage-board-column-1 .stage-board-dot {
  color: #bd82e8;
  background: conic-gradient(currentColor 25%, transparent 0);
}
.stage-board-column-2 .stage-board-dot {
  color: #c99f38;
  background: conic-gradient(currentColor 50%, transparent 0);
}
.stage-board-column-3 .stage-board-dot {
  color: #5d9e6b;
  background: conic-gradient(currentColor 75%, transparent 0);
}
.stage-board-cards {
  display: grid;
  gap: 8px;
  width: 100%;
  padding: 4px 10px 12px;
}
.study-example-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
  padding: 12px;
  border: 1px solid #aeb5bb30;
  border-radius: 8px;
  background: #ffffff30;
  box-shadow: 0 1px 2px #0000000f;
  color: #49535c;
  font-size: 12px;
  line-height: 1.4;
}
.study-example-card strong {
  color: #20262c;
  font-size: 13px;
  font-weight: 500;
}
:global(:root[data-theme='dark']) .stage-board {
  color: #e3ebf2;
}
:global(:root[data-theme='dark']) .stage-board-column {
  background: #272d34;
}
:global(:root[data-theme='dark']) .stage-board-column-1 {
  background: #30283a;
}
:global(:root[data-theme='dark']) .stage-board-column-2 {
  background: #353126;
}
:global(:root[data-theme='dark']) .stage-board-column-3 {
  background: #26362d;
}
:global(:root[data-theme='dark']) .study-example-card {
  color: #bbc8d3;
  background: #ffffff0d;
  border-color: #ffffff18;
}
:global(:root[data-theme='dark']) .study-example-card strong {
  color: #edf2f7;
}
:global(:root[data-theme='dark']) .stage-board-plus {
  color: #bec9d2;
}
</style>
