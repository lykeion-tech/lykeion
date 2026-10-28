<script setup lang="ts">
import Icon from '../components/Icon.vue';
defineProps<{
  modelValue: string;
  busy: boolean;
  blocker?: string;
  error?: string;
  activeRun?: string;
}>();
const emit = defineEmits<{ 'update:modelValue': [string]; send: []; stop: [] }>();
</script>
<template>
  <div class="aristotle-composer">
    <p v-if="blocker" role="status">
      {{ blocker }} <RouterLink to="/settings?tab=machines">Settings</RouterLink>
    </p>
    <p v-if="error" role="alert">{{ error }}</p>
    <div class="composer-card">
      <textarea
        :value="modelValue"
        aria-label="Message Aristotle"
        placeholder="Ask anything about your research…"
        rows="1"
        :disabled="busy"
        @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
        @keydown.enter.exact.prevent="!$event.isComposing && emit('send')"
      />
      <div class="composer-toolbar">
        <button
          v-if="activeRun"
          class="composer-send"
          aria-label="Stop"
          :disabled="busy"
          @click="emit('stop')"
        >
          <span class="stop-square" /></button
        ><button
          v-else
          class="composer-send"
          aria-label="Send"
          :disabled="busy || !!blocker || !modelValue.trim()"
          @click="emit('send')"
        >
          <Icon name="up" :size="16" />
        </button>
      </div>
    </div>
  </div>
</template>
<style scoped>
.aristotle-composer {
  width: min(896px, calc(100% - 48px));
  margin: 12px auto;
  flex: none;
}
.aristotle-composer > p {
  font-size: 12px;
  color: var(--muted);
  margin: 0 0 10px;
}
.aristotle-composer a {
  text-decoration: underline;
}
.composer-card {
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--input);
  padding: 10px 12px;
}
.composer-card textarea {
  width: 100%;
  background: transparent;
  border: 0;
  resize: none;
  outline: none;
  font: inherit;
  line-height: 22px;
  min-height: 28px;
}
.composer-toolbar {
  display: flex;
  align-items: center;
  justify-content: flex-end;
}
</style>

<style scoped>
.stop-square {
  width: 8px;
  height: 8px;
  background: currentColor;
  border-radius: 1px;
}
</style>

<style scoped>
.aristotle-composer .composer-send {
  width: 26px;
  height: 26px;
  min-width: 26px;
  min-height: 26px;
  border-radius: 50%;
  padding: 0;
}
.aristotle-composer .composer-toolbar {
  min-height: 26px;
  padding-top: 0;
}
.aristotle-composer .composer-card textarea {
  height: 28px;
  min-height: 28px;
  padding: 0;
}
.aristotle-composer .composer-card {
  padding: 10px 12px;
}
</style>

<style scoped>
@media (max-width: 700px) {
  .aristotle-composer .composer-card textarea {
    height: auto;
    field-sizing: content;
    max-height: 200px;
  }
}
</style>
