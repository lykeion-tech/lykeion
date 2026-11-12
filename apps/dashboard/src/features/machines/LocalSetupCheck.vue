<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { verifying } from '../../api/auth-loss';
import type { DesktopPrerequisites } from '../../../../desktop/src/daemon-types';

const props = defineProps<{ accountId?: string; signInProblems?: string[] }>();
const providers = [
  {
    id: 'claude',
    name: 'Claude Code',
    status: 'claude auth status',
    login: 'claude auth login',
    reference: 'https://code.claude.com/docs/en/cli-reference',
  },
  {
    id: 'codex',
    name: 'Codex',
    status: 'codex login status',
    login: 'codex login',
    reference: 'https://developers.openai.com/codex/cli/reference/',
  },
] as const;
const reportedProblems = computed(() =>
  verifying.value
    ? []
    : providers.filter((provider) => props.signInProblems?.includes(provider.id)),
);
const desktop = typeof window === 'undefined' ? undefined : window.lykeionDesktop;
const bridge = desktop?.daemon;
const supported = computed(
  () =>
    !!props.accountId &&
    !!bridge?.refreshPrerequisites &&
    (desktop?.platform === 'darwin' || desktop?.platform === 'linux'),
);
const result = ref<DesktopPrerequisites>();
const busy = ref(false);
const error = ref(false);
let visit = 0;
function reset() {
  visit++;
  result.value = undefined;
  error.value = false;
  busy.value = false;
}
watch([() => props.accountId, verifying], reset, { flush: 'sync' });
onUnmounted(reset);
async function refresh() {
  if (!supported.value || busy.value || verifying.value) return;
  const current = ++visit;
  busy.value = true;
  error.value = false;
  result.value = undefined;
  try {
    const facts = await bridge!.refreshPrerequisites();
    if (current === visit) result.value = facts;
  } catch {
    if (current === visit) error.value = true;
  } finally {
    if (current === visit) busy.value = false;
  }
}
</script>
<template>
  <p>
    Install and sign in to at least one tool on this computer using the official
    <a href="https://code.claude.com/docs/en/setup" target="_blank" rel="noopener noreferrer"
      >Claude Code</a
    >
    or
    <a href="https://developers.openai.com/codex/cli/" target="_blank" rel="noopener noreferrer"
      >Codex</a
    >
    guide.
  </p>
  <template v-if="supported">
    <details>
      <summary>Check provider sign-in in your terminal</summary>
      <p v-for="provider in providers" :key="provider.id">
        For {{ provider.name }}, run <code>{{ provider.status }}</code> to check or
        <code>{{ provider.login }}</code> to sign in.
        <a :href="provider.reference" target="_blank" rel="noopener noreferrer"
          >{{ provider.name }} command reference</a
        >
      </p>
    </details>
    <button type="button" class="button" :disabled="busy || verifying" @click="refresh">
      Check setup
    </button>
    <div role="status" aria-live="polite">
      <p v-for="provider in reportedProblems" :key="provider.id">
        This daemon reported a sign-in problem for {{ provider.name }}.
      </p>
      <p v-if="busy">Checking this computer…</p>
      <p v-else-if="error">Setup check did not finish. Select Check setup to try again.</p>
      <p v-else-if="result && !result.cliAvailable">
        The Lykeion runtime is missing or could not be verified. Reinstall the desktop app and check
        again.
      </p>
      <template v-else-if="result?.cliAvailable">
        <p>{{ result.sandbox ? 'Sandbox ready' : 'Sandbox check failed' }}</p>
        <p v-if="!result.sandbox">
          {{
            result.sandboxIssue === 'account_lookup'
              ? 'Lykeion could not verify your Linux account. Check getent and your account/NSS configuration, then check again.'
              : desktop?.platform === 'linux'
                ? 'Check that bubblewrap is installed and can run on this Linux system, then check again.'
                : 'Check that macOS allows the Lykeion sandbox to run, then check again.'
          }}
        </p>
        <ul>
          <li v-for="provider in providers" :key="provider.id">
            {{ provider.name }} · {{ result[provider.id] ? 'Executable found' : 'Not found'
            }}<template v-if="result[provider.id] && !signInProblems?.includes(provider.id)">
              · Sign-in not checked</template
            >
          </li>
        </ul>
      </template>
    </div>
  </template>
</template>
