import { computed, toValue, type MaybeRefOrGetter } from 'vue';
import { useQuery, type QueryClient } from '@tanstack/vue-query';
import { api } from './client';
import { keys } from './keys';
import { useMe } from './queries';
import { workspaceId } from './session';

export type ResearchProfile = {
  identity: string;
  links: string[];
};

export type ResearchProfileState = {
  draft: ResearchProfile | null;
  published: ResearchProfile | null;
  draft_revision: string | null;
  published_revision: string | null;
  workspace_ids: string[];
};

type ResearchProfileResponse = Omit<ResearchProfile, 'identity'> & {
  identity?: string;
  background?: string;
  topics?: string;
  methods?: string;
  collaboration?: string;
};
type ResearchProfileStateResponse = Omit<ResearchProfileState, 'draft' | 'published'> & {
  draft: ResearchProfileResponse | null;
  published: ResearchProfileResponse | null;
};

function readProfile(profile: ResearchProfileResponse | null): ResearchProfile | null {
  if (!profile) return null;
  const { identity, background, topics, methods, collaboration, links } = profile;
  return {
    identity:
      identity ??
      [
        background,
        topics && `Research topics: ${topics}`,
        methods && `Methods and tools: ${methods}`,
        collaboration && `Collaboration interests: ${collaboration}`,
      ]
        .filter(Boolean)
        .join('\n\n'),
    links,
  };
}

function readProfileState(state: ResearchProfileStateResponse): ResearchProfileState {
  return { ...state, draft: readProfile(state.draft), published: readProfile(state.published) };
}

const accountPath = '/api/me/research-profile';
const publicationPath = (workspace: string) =>
  `/api/workspaces/${encodeURIComponent(workspace)}/research-profile`;

export const emptyResearchProfile = (): ResearchProfile => ({
  identity: '',
  links: [],
});

export function hasResearchProfileContent(profile: ResearchProfile) {
  return [profile.identity, ...profile.links].some((value) => value.trim().length > 0);
}

export function useResearchProfile(enabled: MaybeRefOrGetter<boolean> = true) {
  const me = useMe();
  return useQuery({
    queryKey: computed(() => keys.researchProfile(me.data.value?.id ?? '')),
    queryFn: ({ signal }) =>
      api<ResearchProfileStateResponse>('GET', accountPath, undefined, undefined, {
        workspace: '',
        signal,
      }).then(readProfileState),
    enabled: computed(() => toValue(enabled) && Boolean(me.data.value?.id)),
    gcTime: 0,
    staleTime: 0,
  });
}

export function useColleagueResearchProfile(memberId: MaybeRefOrGetter<string>) {
  const me = useMe();
  return useQuery({
    queryKey: computed(() =>
      keys.colleagueProfile(workspaceId.value, toValue(memberId), me.data.value?.id ?? ''),
    ),
    queryFn: async ({ queryKey: [, workspace, , member], signal }) => {
      const { profile } = await api<{ profile: ResearchProfileResponse | null }>(
        'GET',
        `/api/workspaces/${encodeURIComponent(workspace)}/members/${encodeURIComponent(member)}/research-profile`,
        undefined,
        undefined,
        { workspace, signal },
      );
      return { profile: readProfile(profile) };
    },
    enabled: computed(() => Boolean(workspaceId.value && toValue(memberId) && me.data.value?.id)),
    gcTime: 0,
    staleTime: 0,
  });
}

export const saveResearchProfile = (profile: ResearchProfile, { updatePublished = false } = {}) =>
  api<ResearchProfileStateResponse>(
    'PUT',
    updatePublished ? `${accountPath}/save` : accountPath,
    { identity: profile.identity, links: profile.links.map((link) => link.trim()).filter(Boolean) },
    undefined,
    { workspace: '' },
  ).then(readProfileState);

export const shareResearchProfile = (workspace: string, profileRevision: string) =>
  api<ResearchProfileStateResponse>(
    'PUT',
    publicationPath(workspace),
    { profile_revision: profileRevision },
    undefined,
    { workspace },
  ).then(readProfileState);

export function updateResearchProfileCache(
  client: QueryClient,
  account: string,
  result: ResearchProfileState,
  affectedWorkspaces: string[] = [],
) {
  if (client.getQueryData<{ id: string }>(keys.me)?.id !== account) return;
  const queryKey = keys.researchProfile(account);
  void client.cancelQueries({ queryKey }, { revert: false });
  client.setQueryData(queryKey, result);
  for (const workspace of affectedWorkspaces) {
    void client.invalidateQueries({ queryKey: keys.colleagueProfile(workspace, account, account) });
  }
}
