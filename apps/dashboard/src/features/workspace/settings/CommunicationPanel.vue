<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import Icon from '../../../ui/Icon.vue';
import { slackIconPath } from '../../../../../../packages/cli-icons';
import { useSlack, useSlackMutations } from '../../../api/library';
import { useWorkspaces } from '../../../api/queries';
import { workspaceId } from '../../../api/session';
import SlackConnectDialog from './SlackConnectDialog.vue';

const slack = useSlack(),
  workspaces = useWorkspaces(),
  mutations = useSlackMutations();
const owner = computed(
  () => workspaces.data.value?.find((w) => w.id === workspaceId.value)?.role === 'owner',
);
const visibility =
  'Answers in a Slack channel are visible to everyone in that channel, including people outside this workspace.';
const product = 'Slack';
const dialog = ref<'connect' | 'manage' | null>(null);
const error = ref('');
const s = computed(() => slack.data.value);
const revoked = computed(() => s.value?.connected && s.value.status === 'revoked');
const status = computed(() =>
  !s.value?.connected
    ? 'Disconnected'
    : revoked.value
      ? 'Reconnect needed'
      : `Connected · ${s.value.team_name ?? ''}`,
);
const action = computed(() =>
  !s.value?.connected ? 'Connect' : revoked.value ? 'Reconnect' : 'Manage',
);
watch(workspaceId, () => {
  dialog.value = null;
  error.value = '';
});
async function unlink() {
  error.value = '';
  try {
    await mutations.unlinkSlack();
  } catch (e) {
    error.value = (e as Error).message;
  }
}
</script>
<template>
  <section class="communication">
    <p v-if="slack.isError.value" class="inline-error" role="alert">
      Could not load Slack: {{ slack.error.value?.message }}
    </p>
    <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
    <h3>Messaging apps</h3>
    <div class="setting-box">
      <div class="app-row" data-app="slack">
        <svg viewBox="0 0 24 24" width="24" height="24" fill="currentColor" aria-hidden="true">
          <path :d="slackIconPath" />
        </svg>
        <span class="name">Slack<small>{{ status }}</small></span>
        <button
          v-if="owner && s"
          class="button"
          :aria-label="`${action} Slack`"
          @click="dialog = action === 'Manage' ? 'manage' : 'connect'"
        >
          {{ action }}
        </button>
      </div>
    </div>
    <p class="note">{{ visibility }}</p>
    <h3>Who can connect</h3>
    <div class="setting-box">
      <div class="app-row">
        <span class="name">{{ product }}</span><span class="value">Workspace owner only</span>
      </div>
    </div>
    <h3>Your accounts</h3>
    <div class="setting-box">
      <div class="app-row">
        <span class="name">{{ product }}</span>
        <span v-if="s?.my_binding" class="value">Linked as {{ s.my_binding.slack_user_id }}</span>
        <span v-else class="value">Not linked — message Aristotle in Slack to link</span>
        <button
          v-if="s?.my_binding"
          class="button"
          aria-label="Remove Slack link"
          @click="unlink"
        >
          <Icon name="close" :size="14" />Remove
        </button>
      </div>
    </div>
    <SlackConnectDialog
      v-if="dialog"
      :key="workspaceId + dialog"
      :mode="dialog"
      :agent-id="s?.drafting_agent_id"
      @close="dialog = null"
    />
  </section>
</template>
<style scoped>
h3 {
  font-size: 14px;
  font-weight: 600;
  line-height: 20px;
  margin: 20px 2px 12px;
}
h3:first-of-type {
  margin-top: 0;
}
.value {
  color: var(--muted);
  text-align: right;
  min-width: 0;
}
.note {
  color: var(--muted);
  font-size: 13px;
  line-height: 18px;
  margin: 10px 2px 0;
}
</style>
<style scoped src="./integration-settings.css"></style>
