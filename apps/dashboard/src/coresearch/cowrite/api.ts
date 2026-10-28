import { computed, toValue, type MaybeRefOrGetter } from 'vue';
import { useQuery } from '@tanstack/vue-query';
import { api } from '../../api/client';
import { workspaceId } from '../../api/session';
import { keys, projectPath } from '../api';
import type {
  Doc,
  DocStep,
  Change,
  Comment,
  Build,
  Version,
  Compute,
  Template,
  Presence,
} from './types';
export const docPath = (project: string, file: string) =>
  `${projectPath(project)}/docs/${encodeURIComponent(file)}`;
type Source<T> = MaybeRefOrGetter<T>;
function useResource<T>(
  project: Source<string>,
  key: () => readonly unknown[],
  path: () => string,
  enabled: () => boolean = () => true,
  interval?: number,
) {
  return useQuery({
    queryKey: computed(key),
    enabled: computed(() => !!workspaceId.value && !!toValue(project) && enabled()),
    queryFn: ({ signal }) =>
      api<T>('GET', path(), undefined, undefined, { signal, workspace: workspaceId.value }),
    ...(interval ? { refetchInterval: interval } : {}),
  });
}
export function useDoc(project: Source<string>, file: Source<string>) {
  return useResource<Doc>(
    project,
    () => keys.cowriteDoc(workspaceId.value, toValue(project), toValue(file)),
    () => docPath(toValue(project), toValue(file)),
    () => !!toValue(file),
  );
}
export function useDocSteps(
  project: Source<string>,
  file: Source<string>,
  after: Source<number>,
  open: Source<boolean> = true,
) {
  return useResource<DocStep[]>(
    project,
    () => keys.cowriteDocSteps(workspaceId.value, toValue(project), toValue(file), toValue(after)),
    () => `${docPath(toValue(project), toValue(file))}/steps?after=${toValue(after)}`,
    () => !!toValue(file) && toValue(open),
  );
}
export function useChanges(project: Source<string>, file: Source<string>) {
  return useResource<Change[]>(
    project,
    () => keys.cowriteChanges(workspaceId.value, toValue(project), toValue(file)),
    () => `${docPath(toValue(project), toValue(file))}/changes`,
    () => !!toValue(file),
  );
}
export function useComments(project: Source<string>, file: Source<string>) {
  return useResource<Comment[]>(
    project,
    () => keys.cowriteComments(workspaceId.value, toValue(project), toValue(file)),
    () => `${docPath(toValue(project), toValue(file))}/comments`,
    () => !!toValue(file),
  );
}
export function useBuilds(project: Source<string>) {
  return useResource<Build[]>(
    project,
    () => keys.cowriteBuilds(workspaceId.value, toValue(project)),
    () => `${projectPath(toValue(project))}/builds`,
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
export function usePresence(
  project: Source<string>,
  file: Source<string>,
  open: Source<boolean> = true,
) {
  return useResource<Presence[]>(
    project,
    () => keys.cowritePresence(workspaceId.value, toValue(project), toValue(file)),
    () => `${docPath(toValue(project), toValue(file))}/presence`,
    () => !!toValue(file) && toValue(open),
    1000,
  );
}
