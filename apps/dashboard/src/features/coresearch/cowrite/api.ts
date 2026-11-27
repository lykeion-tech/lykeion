import { computed, toValue, type MaybeRefOrGetter } from 'vue';
import { useQuery } from '@tanstack/vue-query';
import { api } from '../../../api/client';
import { workspaceId } from '../../../api/session';
import { keys, projectPath } from '../api';
import type {
  Comment,
  Build,
  Version,
  Compute,
  Template,
} from './types';
export const docPath = (project: string, file: string) =>
  `${projectPath(project)}/docs/${encodeURIComponent(file)}`;
type Source<T> = MaybeRefOrGetter<T>;
function useResource<T>(
  project: Source<string>,
  key: () => readonly unknown[],
  path: () => string,
  enabled: () => boolean = () => true,
  interval?: number | ((data: T | undefined) => number | false),
) {
  return useQuery({
    queryKey: computed(key),
    enabled: computed(() => !!workspaceId.value && !!toValue(project) && enabled()),
    queryFn: ({ signal }) =>
      api<T>('GET', path(), undefined, undefined, { signal, workspace: workspaceId.value }),
    ...(interval
      ? {
          refetchInterval:
            typeof interval === 'number'
              ? interval
              : (query: { state: { data: T | undefined } }) => interval(query.state.data),
        }
      : {}),
  });
}
export function useComments(project: Source<string>, file: Source<string>) {
  return useResource<Comment[]>(
    project,
    () => keys.cowriteComments(workspaceId.value, toValue(project), toValue(file)),
    () => `${docPath(toValue(project), toValue(file))}/comments`,
    () => !!toValue(file),
  );
}
// Builds change on their own only while one is queued or running; settled
// builds are refreshed by build events.
export const buildPollInterval = (builds: Build[] | undefined) =>
  builds?.some((b) => b.status === 'queued' || b.status === 'running') ? 1500 : false;
export function useBuilds(project: Source<string>) {
  return useResource<Build[]>(
    project,
    () => keys.cowriteBuilds(workspaceId.value, toValue(project)),
    () => `${projectPath(toValue(project))}/builds`,
    () => true,
    buildPollInterval,
  );
}
export function useVersions(project: Source<string>) {
  return useResource<Version[]>(
    project,
    () => keys.cowriteVersions(workspaceId.value, toValue(project)),
    () => `${projectPath(toValue(project))}/versions`,
  );
}
export function useCompute(project: Source<string>) {
  return useResource<Compute>(
    project,
    () => keys.cowriteCompute(workspaceId.value, toValue(project)),
    () => `${projectPath(toValue(project))}/compute`,
  );
}
export function useTemplates(project: Source<string> = 'new') {
  return useResource<Template[]>(
    project,
    () => keys.cowriteTemplates(workspaceId.value, toValue(project)),
    () => '/api/coresearch/templates',
  );
}
