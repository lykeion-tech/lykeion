import { computed, ref, watch, onScopeDispose } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { workspaceId } from '../api/session';
import { useWorkspaces } from '../api/queries';
import { useLibraryMutations } from '../api/library';
import { keys } from '../api/keys';
export function useAvailabilityChanges() {
  const workspaces = useWorkspaces(),
    mutations = useLibraryMutations(),
    client = useQueryClient();
  const role = computed(() => workspaces.data.value?.find((w) => w.id === workspaceId.value)?.role);
  const allowed = computed(() => role.value === 'owner' || role.value === 'admin');
  const catalogueAllowed = computed(() => role.value === 'owner');
  const pending = ref(''),
    error = ref('');
  let visit = 0;
  watch(
    workspaceId,
    () => {
      visit++;
      pending.value = '';
      error.value = '';
    },
    { flush: 'sync' },
  );
  onScopeDispose(() => visit++);
  async function toggle(
    kind: 'skill' | 'mcp' | 'catalogue',
    id: string,
    revision: number,
    enabled: boolean,
  ) {
    if (pending.value || !(kind === 'catalogue' ? catalogueAllowed.value : allowed.value)) return;
    const ws = workspaceId.value,
      v = visit;
    pending.value = id;
    error.value = '';
    try {
      if (kind === 'catalogue') await mutations.setConnector(id, { revision, enabled });
      else await mutations.setAvailability(kind, id, { revision, enabled });
    } catch (e) {
      if (v === visit && ws === workspaceId.value) {
        error.value = (e as Error).message;
        await client.invalidateQueries({
          queryKey:
            kind === 'skill'
              ? keys.skills(ws)
              : kind === 'mcp'
                ? keys.workspaceMCP(ws)
                : keys.connectors(ws),
        });
      }
    } finally {
      if (v === visit && ws === workspaceId.value) pending.value = '';
    }
  }
  return { allowed, catalogueAllowed, pending, error, toggle };
}
