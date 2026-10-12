import { computed, reactive, ref, shallowRef } from 'vue';
import type { ResearchService, WorkspaceSnapshot } from '../../../../packages/contracts/src/research';
import { HttpResearchService, LocalResearchService } from './research';
let endpoint = '';
let startupError = '';
try { endpoint = localStorage.getItem('lykeion.api-url') ?? ''; }
catch { startupError = 'Connection preferences could not be read. Local storage may be unavailable.'; }
let initialService: ResearchService = new LocalResearchService();
try { if (endpoint) initialService = new HttpResearchService(endpoint); }
catch { startupError = 'The saved API URL is invalid. Local drafts are available; correct the URL in Connection settings.'; }
export const apiUrl = ref(endpoint);
export const service = shallowRef<ResearchService>(initialService);
export const state = reactive<{ data: WorkspaceSnapshot | null; loading: boolean; error: string }>({ data: null, loading: false, error: startupError });
let generation = 0;
let connectionGeneration = 0;
let refreshController: AbortController | undefined;
let connectController: AbortController | undefined;
export async function refresh() {
  const current = ++generation;
  const selected = service.value;
  refreshController?.abort();
  const controller = refreshController = new AbortController();
  state.loading = true; state.error = startupError;
  try { const data = await selected.load(controller.signal); if (current === generation && selected === service.value) state.data = data; }
  catch (error) { if (current === generation && !controller.signal.aborted) { state.data = null; state.error = error instanceof Error ? error.message : 'Could not load the workspace.'; } }
  finally { if (current === generation) state.loading = false; }
}
export async function connect(url: string) {
  const cleaned = url.trim();
  const next = cleaned ? new HttpResearchService(cleaned) : new LocalResearchService();
  const current = ++connectionGeneration;
  connectController?.abort();
  const controller = connectController = new AbortController();
  const data = await next.load(controller.signal);
  if (current !== connectionGeneration) return;
  // Keep the previous service active unless both the probe and preference write succeed.
  try { localStorage.setItem('lykeion.api-url', cleaned); }
  catch { throw new Error('Could not save connection preferences. The existing connection is unchanged.'); }
  generation++; refreshController?.abort(); startupError = '';
  service.value = next; apiUrl.value = cleaned; state.data = data; state.error = ''; state.loading = false;
}
export const workspaceName = computed(() => state.data?.workspace.name ?? 'Lykeion');
export const notice = ref('');
let noticeTimer: ReturnType<typeof setTimeout>;
export function notify(message: string) { notice.value = message; clearTimeout(noticeTimer); noticeTimer = setTimeout(() => { notice.value = ''; }, 7000); }
