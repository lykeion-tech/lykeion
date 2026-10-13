<script setup lang="ts">
import { computed, ref } from 'vue';
import { matchesQuery } from '../lib/search';
import { state } from '../services/workspace';
import Modal from './Modal.vue';
import Icon from './Icon.vue';
const status = ref('Waiting or executing'),
  source = ref('all'),
  trigger = ref('all'),
  agent = ref('all'),
  search = ref(''),
  picker = ref(false),
  taskQuery = ref(''),
  taskId = ref(''),
  date = ref(''),
  error = ref('');
const matches = computed(() =>
  taskQuery.value.trim()
    ? (state.data?.inquiries ?? []).filter((x) => matchesQuery(x.title, taskQuery.value))
    : [],
);
const task = computed(() => state.data?.inquiries.find((x) => x.id === taskId.value));
function open() {
  taskQuery.value = '';
  taskId.value = '';
  error.value = '';
  picker.value = true;
}
defineExpose({ open });
</script>
<template>
  <div class="wakeups">
    <div class="wakeup-toolbar">
      <div class="wakeup-statuses">
        <button
          v-for="tab in ['Waiting or executing', 'Paused', 'Turned off', 'Ended', 'All']"
          :key="tab"
          :class="{ active: status === tab }"
          @click="status = tab"
        >
          {{ tab }} <span>0</span>
        </button>
      </div>
      <div class="push-right wakeup-filters">
        <select v-model="source" aria-label="Wakeup source">
          <option value="all">Source: All</option>
          <option value="manual">Manual</option>
          <option value="agent">Agent</option>
        </select>
        <select v-model="trigger" aria-label="Wakeup trigger">
          <option value="all">All triggers</option>
          <option value="schedule">Schedule</option>
          <option value="webhook">Webhook</option>
        </select>
        <select v-model="agent" aria-label="Wakeup agent">
          <option value="all">All agents</option>
          <option v-for="item in state.data?.agents" :key="item.id" :value="item.id">
            {{ item.name }}
          </option>
        </select>
        <label>
          <Icon name="search" :size="14" />
          <input
            v-model="search"
            placeholder="Search tasks or agents"
            aria-label="Search wakeups"
          />
        </label>
      </div>
    </div>
    <div class="wakeup-empty">
      <span>
        <Icon name="bell" :size="16" />
      </span>
      <p>No matching wakeups</p>
    </div>
    <footer>
      <small>Page 1 · 0 results</small>
      <button disabled>Previous</button>
      <button disabled>Next</button>
    </footer>
    <Modal
      v-if="picker"
      title="Choose task for wakeup"
      class="wakeup-picker"
      @close="picker = false"
    >
      <label>
        <Icon name="search" :size="14" />
        <input
          v-model="taskQuery"
          placeholder="Search tasks..."
          aria-label="Search tasks for wakeup"
          autofocus
          @keydown.enter.prevent="matches[0] && ((taskId = matches[0].id), (picker = false))"
        />
      </label>
      <button
        v-for="item in matches"
        :key="item.id"
        @click="
          taskId = item.id;
          picker = false;
        "
      >
        <Icon name="tasks" :size="14" />{{ item.title }}
      </button>
      <p v-if="!matches.length">{{ taskQuery ? 'No matching tasks' : 'Type to search tasks' }}</p>
    </Modal>
    <Modal v-if="taskId" title="New task wakeup" class="wakeup-setup" @close="taskId = ''">
      <form
        @submit.prevent="
          error = 'Task wakeups require a scheduling adapter. No wakeup has been scheduled.'
        "
      >
        <h2>New wakeup</h2>
        <p>{{ task?.title }}</p>
        <label>When<input v-model="date" type="datetime-local" required /> </label>
        <p v-if="error" class="wakeup-error" role="alert">{{ error }}</p>
        <footer>
          <button type="button" class="button" @click="taskId = ''">Cancel</button>
          <button class="button primary">Create wakeup</button>
        </footer>
      </form>
    </Modal>
  </div>
</template>
<style scoped>
.wakeups {
  height: calc(100% - 36px);
  display: flex;
  flex-direction: column;
}
.wakeup-toolbar {
  padding: 12px 16px;
  display: flex;
  align-items: center;
  gap: 12px;
}
.wakeup-statuses {
  display: flex;
  padding: 2px;
  border-radius: 7px;
  background: var(--fill-control);
  white-space: nowrap;
}
.wakeup-statuses > button {
  height: 28px;
  padding: 0 10px;
  border-radius: 6px;
  font-size: 12px;
  color: var(--muted);
}
.wakeup-statuses > button.active {
  background: var(--fill-card);
  box-shadow: 0 1px 2px #0001;
  color: var(--text);
}
.wakeup-filters {
  display: flex;
  gap: 8px;
}
.wakeup-filters select,
.wakeup-filters label {
  display: flex;
  align-items: center;
  gap: 8px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--surface);
  height: 30px;
  padding: 0 10px;
  font-size: 13px;
  color: var(--muted);
}
.wakeup-filters input {
  background: none;
  border: 0;
  outline: 0;
  min-width: 0;
  width: 164px;
  font-size: 13px;
}
.wakeup-empty {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-direction: column;
  gap: 18px;
}
.wakeup-empty > span {
  width: 48px;
  height: 48px;
  display: grid;
  place-items: center;
  background: var(--fill-avatar);
  border-radius: 50%;
  color: var(--muted);
}
.wakeup-empty p {
  font-size: 14px;
}
.wakeups > footer {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: 20px;
  border-top: 1px solid var(--border);
  height: 46px;
  padding: 0 24px;
  color: var(--muted);
  font-size: 12px;
}
.wakeups > footer button {
  font-size: 12px;
}
.wakeup-picker {
  width: 386px;
  background: var(--fill-menu);
  position: fixed;
  top: 33.3vh;
  bottom: auto;
  margin-top: 0;
  padding: 8px;
}
.wakeup-picker > label {
  display: flex;
  align-items: center;
  gap: 8px;
  background: var(--fill-field);
  border-radius: 8px;
  padding: 7px 10px;
  color: var(--text-faint);
}
.wakeup-picker input {
  background: none;
  border: 0;
  outline: 0;
  width: 100%;
  font-size: 14px;
}
.wakeup-picker > p {
  padding: 24px 12px;
  text-align: center;
  color: var(--muted);
  font-size: 14px;
}
.wakeup-picker > button {
  display: flex;
  gap: 8px;
  align-items: center;
  text-align: left;
  width: 100%;
  padding: 10px;
  border-radius: 6px;
}
.wakeup-picker > button:hover {
  background: var(--input);
}
.wakeup-setup {
  width: 480px;
}
.wakeup-setup form {
  padding: 24px;
  display: flex;
  flex-direction: column;
  gap: 20px;
}
.wakeup-setup label {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.wakeup-setup input {
  padding: 8px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--surface);
}
.wakeup-setup footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
.wakeup-error {
  font-size: 13px;
  color: var(--error-text-wakeup);
}
@media (max-width: 1500px) {
  .wakeup-toolbar {
    flex-wrap: wrap;
  }
  .wakeup-filters {
    margin-left: 0;
  }
}
</style>
