import { ref, watch } from 'vue';
import { onAuthenticationLost, onAuthenticationRestored } from '../api/auth-loss';
import { readJSON, readText, writeJSON, writeText } from '../storage';

export type Tab = {
  id: number;
  path: string;
  title: string;
  icon: string;
  history: string[];
  index: number;
  pinned: boolean;
};

export const validPath = (value: unknown): value is string =>
  typeof value === 'string' && value.startsWith('/') && !value.startsWith('//');

export const accountViewBlocked = (isPublic: boolean, id: string | undefined, switching: boolean) =>
  !isPublic && (switching || !id);

export async function verifyAccountSwitch(
  id: string,
  current: () => boolean,
  reset: () => Promise<void>,
  identify: () => Promise<string | undefined>,
  navigate: () => Promise<boolean>,
): Promise<boolean> {
  await reset();
  if (!current()) return false;
  const verified = await identify();
  if (!current()) return false;
  if (verified !== id) throw new Error('Identity could not be verified');
  const safe = await navigate();
  if (!current()) return false;
  if (!safe) throw new Error('Navigation was blocked');
  return true;
}

function restored(t: unknown): Tab[] {
  try {
    if (
      Array.isArray(t) &&
      t.length &&
      t.every(
        (x) =>
          Number.isFinite(x.id) &&
          validPath(x.path) &&
          typeof x.title === 'string' &&
          typeof x.icon === 'string' &&
          Array.isArray(x.history) &&
          x.history.length > 0 &&
          x.history.every(validPath) &&
          Number.isInteger(x.index) &&
          x.index >= 0 &&
          x.index < x.history.length,
      )
    )
      return t.map((x) => ({
        ...x,
        title:
          x.title === 'Researches' && /^\/projects(?:\/|[?#]|$)/.test(x.path)
            ? 'Projects'
            : x.title,
        pinned: x.pinned === true,
      }));
  } catch {
    // An entry that is not an object fails the shape check; the session starts afresh.
  }
  return [];
}

// Never expose saved titles until the server has identified their owner.
const tabs = ref<Tab[]>([]);
const selected = ref(0);
let account: string | null = null;
let restoring = false;
let pendingSwitch = false;
let nextId = Date.now() + 1;
const legacy = readText('lykeion.tabs', 'session');
let legacyUnowned = !!legacy && legacy !== '[]';
const prior = readJSON<Record<string, unknown> | null>('lykeion.workspace-tabs', null, 'session');
// Replacing the legacy value first can free space even when storage is full.
if (legacyUnowned) writeJSON('lykeion.tabs', { pendingSwitch: true }, 'session');
if (
  !legacyUnowned ||
  writeJSON(
    'lykeion.workspace-tabs',
    { ...(prior && typeof prior === 'object' ? prior : {}), pendingSwitch: true },
    'session',
  )
) {
  writeJSON('lykeion.tabs', [], 'session');
  writeText('lykeion.active-tab', '0', 'session');
}
function hasPendingSwitch() {
  return (
    legacyUnowned ||
    readJSON<{ pendingSwitch?: unknown }>('lykeion.workspace-tabs', {}, 'session').pendingSwitch ===
      true
  );
}
function persist() {
  if (account && !restoring)
    writeJSON(
      'lykeion.workspace-tabs',
      { account, tabs: tabs.value, selected: selected.value, pendingSwitch },
      'session',
    );
}
watch(selected, persist, { flush: 'sync' });
watch(tabs, persist, { deep: true, flush: 'sync' });

function clearAccount() {
  account = null;
  pendingSwitch = false;
  legacyUnowned = false;
  tabs.value = [];
  selected.value = 0;
  writeJSON('lykeion.workspace-tabs', null, 'session');
  writeJSON('lykeion.tabs', [], 'session');
}

function setAccount(id: string) {
  if (!id || account === id) return false;
  const saved = account
    ? null
    : readJSON<{
        account?: unknown;
        tabs?: unknown;
        selected?: unknown;
        pendingSwitch?: unknown;
      } | null>('lykeion.workspace-tabs', null, 'session');
  const changed =
    account !== null ||
    legacyUnowned ||
    (typeof saved?.account === 'string' && saved.account !== id) ||
    saved?.pendingSwitch === true;
  restoring = true;
  account = id;
  legacyUnowned = false;
  pendingSwitch = changed;
  tabs.value = saved?.account === id ? restored(saved.tabs) : [];
  selected.value = tabs.value.some((t) => t.id === saved?.selected)
    ? (saved!.selected as number)
    : (tabs.value[0]?.id ?? 0);
  nextId = Math.max(Date.now(), ...tabs.value.map((t) => t.id)) + 1;
  restoring = false;
  persist();
  return changed;
}

function confirmAccountSwitch() {
  pendingSwitch = false;
  persist();
}

onAuthenticationLost(clearAccount);
onAuthenticationRestored(clearAccount);

// Adds a tab and selects it; WorkspaceTabs navigates to whichever tab is selected.
function openTab(path = '/studies', title = 'Studies', icon = 'tasks') {
  if (!validPath(path)) return;
  const t: Tab = { id: nextId++, path, title, icon, history: [path], index: 0, pinned: false };
  tabs.value.push(t);
  selected.value = t.id;
}

// Starts the strip with one tab when the session had none.
function ensureTab(path: string, title: string, icon: string) {
  if (!tabs.value.length) openTab(path, title, icon);
}

// The tab strip and Coresearch share one account-bound session.
export function useWorkspaceTabs() {
  return {
    tabs,
    selected,
    openTab,
    ensureTab,
    setAccount,
    clearAccount,
    confirmAccountSwitch,
    hasPendingSwitch,
    canRestoreAccount: (id: string) => {
      if (legacyUnowned || pendingSwitch || (account && account !== id)) return false;
      if (readText('lykeion.workspace-tabs', 'session') === null) return true;
      const saved = readJSON<{ account?: unknown; pendingSwitch?: unknown } | null>(
        'lykeion.workspace-tabs',
        null,
        'session',
      );
      return saved?.account === id && saved.pendingSwitch === false;
    },
    currentAccount: () => account ?? undefined,
  };
}
