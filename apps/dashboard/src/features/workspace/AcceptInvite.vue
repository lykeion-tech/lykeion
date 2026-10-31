<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { useQueryClient } from '@tanstack/vue-query';
import { api, ApiError } from '../../api/client';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { writeText } from '../../storage';

const props = defineProps<{ token: string }>();
const router = useRouter();
const client = useQueryClient();
const error = ref('');

// Signs in first when needed: the token waits in this tab's session storage
// and sign-in comes back here.
onMounted(async () => {
  try {
    const out = await api<{ workspace_id: string }>(
      'POST',
      `/api/invitations/${encodeURIComponent(props.token)}/accept`,
    );
    await client.invalidateQueries({ queryKey: keys.workspaces });
    workspaceId.value = out.workspace_id;
    await router.replace('/studies');
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) {
      writeText('lykeion.invite', props.token, 'session');
      await router.replace('/signin');
      return;
    }
    error.value =
      e instanceof ApiError && e.status === 404
        ? 'This invitation was not found. It may have expired, been revoked, or been sent to another address.'
        : (e as Error).message;
  }
});
</script>
<template>
  <div class="center-empty">
    <template v-if="error"
      ><p role="alert">{{ error }}</p>
      <RouterLink class="button" to="/studies">Open Lykeion</RouterLink></template
    >
    <span v-else role="status">Accepting the invitation…</span>
  </div>
</template>
