<script setup lang="ts">
import { computed, ref } from 'vue';
import { useRoute } from 'vue-router';
import { useSlack, useSlackMutations } from '../../api/library';

const route = useRoute();
const one = (v: unknown) => (Array.isArray(v) ? v[0] : v) || '';
const workspace = computed(() => String(one(route.query.workspace)));
const token = computed(() => String(one(route.query.token)));
const slack = useSlack(workspace);
const { linkSlack } = useSlackMutations();
const incomplete = computed(() => !token.value || !workspace.value);
const state = ref<'ask' | 'busy' | 'done'>('ask');
const error = ref('');
const team = computed(() => slack.data.value?.team_name ?? 'Slack');
async function link() {
  if (state.value === 'busy') return;
  state.value = 'busy';
  error.value = '';
  try {
    await linkSlack(token.value, workspace.value);
    state.value = 'done';
  } catch (e) {
    error.value = (e as Error).message;
    state.value = 'ask';
  }
}
</script>
<template>
  <div class="center-empty">
    <p v-if="state === 'done'" role="status">
      Slack account linked. You can go back to Slack and message Aristotle.
    </p>
    <p v-else-if="incomplete" role="alert">
      This link is incomplete. Message Aristotle in Slack for a new one.
    </p>
    <template v-else>
      <h1>Link Slack account to {{ team }} in this workspace?</h1>
      <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
      <div class="actions">
        <button class="button primary" :disabled="state === 'busy' || !token" @click="link">
          Link
        </button>
        <RouterLink class="button" to="/studies">Cancel</RouterLink>
      </div>
    </template>
  </div>
</template>
<style scoped>
h1 {
  font-size: 18px;
  font-weight: 600;
  margin: 0 0 12px;
}
.actions {
  display: flex;
  gap: 8px;
  justify-content: center;
}
</style>
