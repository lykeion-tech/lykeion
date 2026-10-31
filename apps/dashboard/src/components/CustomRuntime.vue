<script setup lang="ts">
import { computed, ref } from 'vue';
import { state, notify } from '../services/workspace';
import Modal from './Modal.vue';
import Icon from './Icon.vue';
defineProps<{ machineName: string }>();
const emit = defineEmits<{ close: [] }>();
const step = ref(1),
  provider = ref(''),
  name = ref(''),
  command = ref(''),
  description = ref(''),
  error = ref('');
const providers = computed(() => [
  ...new Set(
    state.data?.runtimes.map((x) => x.provider).filter((x): x is string => Boolean(x)) ?? [],
  ),
]);
</script>
<template>
  <Modal title="New custom runtime" class="custom-runtime" @close="emit('close')">
    <form
      @submit.prevent="
        error =
          'Runtime registration requires a connected daemon adapter. No command has been executed.'
      "
    >
      <header>
        <div>
          <Icon name="network" />
          <h2>New custom runtime</h2>
          <button
            type="button"
            class="icon-button push-right"
            aria-label="Close"
            @click="emit('close')"
          >
            <Icon name="close" />
          </button>
        </div>
        <p>
          Set up a custom command from {{ machineName }}. The definition syncs workspace-wide and
          registers only on machines where that command is available.
        </p>
        <button
          type="button"
          class="guide-link"
          @click="
            notify(
              'Custom-runtime setup documentation will be supplied by the connected daemon adapter.',
            )
          "
        >
          View setup guide <Icon name="external" :size="12" />
        </button>
      </header>
      <main>
        <small>STEP {{ step }} OF 2</small>
        <template v-if="step === 1">
          <h3>Choose a base runtime</h3>
          <p>Select the CLI this command is compatible with.</p>
          <div class="provider-grid">
            <button
              v-for="item in providers"
              :key="item"
              type="button"
              @click="
                provider = item;
                step = 2;
              "
            >
              <Icon name="command" />{{ item }}
            </button>
          </div>
          <p v-if="!providers.length">
            No runtime providers have been reported by the control plane.
          </p>
          <label>Base runtime<input v-model="provider" placeholder="Provider identifier" /> </label>
          <button type="button" class="button" :disabled="!provider.trim()" @click="step = 2">
            Continue
          </button>
        </template>
        <template v-else>
          <h3>Configure the runtime</h3>
          <label
            >Base runtime<input :value="provider" readonly />
            <small>The base runtime cannot be changed after creation.</small>
          </label>
          <label
            >Display name<input
              v-model="name"
              required
              :placeholder="'e.g. My custom ' + provider + '…'"
            />
          </label>
          <label
            >Command<input
              v-model="command"
              required
              placeholder="e.g. agent --model …"
              class="command-input"
            />
          </label>
          <label
            >Description<textarea
              v-model="description"
              placeholder="What this runtime is for (optional)…"
            />
          </label>
        </template>
      </main>
      <p v-if="error" class="custom-runtime-error" role="alert">{{ error }}</p>
      <footer>
        <button v-if="step === 2" type="button" class="back-button" @click="step = 1">
          <Icon name="left" :size="14" />Back
        </button>
        <button type="button" class="button compact push-right" @click="emit('close')">
          Cancel
        </button>
        <button v-if="step === 2" class="button primary compact">Create runtime</button>
      </footer>
    </form>
  </Modal>
</template>
<style scoped>
.custom-runtime {
  width: 676px;
  background: var(--fill-menu);
}
.custom-runtime form {
  display: flex;
  flex-direction: column;
  max-height: 85vh;
}
.custom-runtime header {
  padding: 20px 24px;
  border-bottom: 1px solid #fff4;
}
.custom-runtime header > div {
  display: flex;
  align-items: center;
  gap: 8px;
}
.custom-runtime h2 {
  font-size: 16px;
  font-weight: 500;
}
.custom-runtime p {
  font-size: 12px;
  color: var(--muted);
  line-height: 17px;
  margin-top: 12px;
}
.guide-link {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  text-decoration: underline;
  padding: 0;
  margin-top: 4px;
}
.custom-runtime main {
  padding: 20px 24px;
  display: flex;
  flex-direction: column;
  gap: 16px;
  overflow: auto;
}
.custom-runtime main > small {
  font-size: 11px;
  color: var(--muted);
}
.custom-runtime main h3 {
  font-size: 14px;
  font-weight: 400;
}
.custom-runtime main > p {
  margin-top: -8px;
}
.custom-runtime label {
  display: flex;
  flex-direction: column;
  gap: 7px;
  font-size: 12px;
  color: var(--muted);
}
.custom-runtime input,
.custom-runtime textarea {
  background: transparent;
  border: 1px solid #fff4;
  border-radius: 6px;
  padding: 9px 10px;
  width: 100%;
  font-size: 14px;
}
.custom-runtime textarea {
  height: 64px;
  resize: none;
}
.custom-runtime label small {
  font-size: 11px;
}
.command-input {
  font-family: monospace;
}
.provider-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
}
.provider-grid > button {
  display: flex;
  align-items: center;
  gap: 8px;
  text-align: left;
  padding: 12px;
  background: var(--surface);
  border-radius: 6px;
  font-size: 14px;
}
.custom-runtime footer {
  border-top: 1px solid #fff4;
  padding: 12px 24px;
  display: flex;
  align-items: center;
  gap: 8px;
}
.back-button {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 14px;
  padding: 0;
}
.custom-runtime-error {
  padding: 0 24px 16px;
  color: var(--error-text-custom-runtime) !important;
}
.custom-runtime main > .button {
  align-self: flex-start;
}
</style>
