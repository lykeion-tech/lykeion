<script lang="ts">
import type { EditorState as CMState } from '@codemirror/state';
const retained = new Map<string, { state: CMState; unregister: () => void; release: () => void }>();
</script>
<script setup lang="ts">
import { ref, watch, onMounted, onBeforeUnmount } from 'vue';
import { EditorState, Compartment, StateEffect, StateField, type Range } from '@codemirror/state';
import {
  EditorView,
  Decoration,
  WidgetType,
  lineNumbers,
  gutter,
  GutterMarker,
  highlightActiveLine,
  drawSelection,
  keymap,
} from '@codemirror/view';
import { defaultKeymap, history, historyKeymap, undo, redo } from '@codemirror/commands';
import { indentOnInput, StreamLanguage, syntaxHighlighting } from '@codemirror/language';
import { stex } from '@codemirror/legacy-modes/mode/stex';
import { search, searchKeymap, openSearchPanel } from '@codemirror/search';
import { autocompletion, completionKeymap } from '@codemirror/autocomplete';
import { collab, getClientID, sendableUpdates, getSyncedVersion } from '@codemirror/collab';
import { api } from '../../api/client';
import { useQueryClient } from '@tanstack/vue-query';
import { keys } from '../../api/keys';
import type { Me } from '../../api/types';
import { authenticationIsLost } from '../../api/auth-loss';
import { workspaceId } from '../../api/session';
import { useMe } from '../../api/queries';
import { registerDraftVisit } from '../../state/resource-draft';
import { docPath } from './api';
import { projectPath } from '../api';
import { createCollaboration, collaborativeState } from './collab';
import { trackedChanges, setChangeRows, mapChangeRows } from './trackedChanges';
import { latexHighlight } from './lib/latex-highlight';
import { latexCompletions } from './lib/latex-completion';
import type { TexError } from './lib/manuscript';
import type { Change, Doc, Presence } from './types';
import type { SelectionTarget } from './SelectionToolbar.vue';
const props = defineProps<{
  projectId: string;
  fileId: string;
  path: string;
  disabled?: boolean;
  bibKeys?: { key: string; title: string }[];
  labels?: string[];
  errors?: TexError[];
}>();
const emit = defineEmits<{
  text: [string];
  cursor: [number];
  selection: [SelectionTarget | undefined];
  error: [string];
  build: [];
  ready: [];
}>();
const setErrors = StateEffect.define<Map<number, string>>();
const errorLines = StateField.define<Map<number, string>>({
  create: () => new Map(),
  update(value, tr) {
    for (const e of tr.effects) if (e.is(setErrors)) value = e.value;
    return value;
  },
});
class ErrorDot extends GutterMarker {
  constructor(private message: string) {
    super();
  }
  eq(other: ErrorDot) {
    return other.message === this.message;
  }
  toDOM() {
    const dot = document.createElement('span');
    dot.className = 'cm-error';
    dot.title = this.message;
    dot.textContent = '●';
    return dot;
  }
}
const errorGutter = gutter({
  class: 'cm-error-gutter',
  lineMarker(live, line) {
    const message = live.state.field(errorLines).get(live.state.doc.lineAt(line.from).number);
    return message === undefined ? null : new ErrorDot(message);
  },
  lineMarkerChange: (u) => u.startState.field(errorLines) !== u.state.field(errorLines),
});
function updateErrors() {
  const lines = new Map<number, string>();
  for (const e of props.errors ?? [])
    if (e.file === props.path && e.line !== undefined && !lines.has(e.line))
      lines.set(e.line, e.message);
  view?.dispatch({ effects: setErrors.of(lines) });
}
watch(() => [props.errors, props.path], updateErrors, { deep: true });
const client = useQueryClient();
const me = useMe(),
  host = ref<HTMLElement>();
const workspace = workspaceId.value,
  person = me.data.value?.id,
  project = props.projectId,
  file = props.fileId;
const identity = `${person}:${workspace}:${project}:${file}`;
let view: EditorView | undefined,
  state: CMState | undefined,
  sync: ReturnType<typeof createCollaboration> | undefined,
  interval: ReturnType<typeof setInterval> | undefined,
  cursorTimer: ReturnType<typeof setTimeout> | undefined,
  alive = true,
  refreshing = false,
  lastCursor = 0;
const readOnly = new Compartment();
const options = { workspace };
function scope(): 'active' | 'recovering' | 'detached' {
  // The observer may be disposed; read the current cache, never its captured ref.
  const current = client.getQueryData<Me>(keys.me)?.id;
  if (authenticationIsLost() || !current) return 'recovering';
  if (current !== person) return 'detached';
  if (!workspaceId.value) return 'recovering';
  return workspaceId.value === workspace ? 'active' : 'detached';
}
function authorized() {
  if (scope() !== 'active')
    throw new Error('Return to this member and lab to save the pending document');
}
function fail(e: unknown) {
  emit('error', e instanceof Error ? e.message : String(e));
}
async function flush() {
  if (!sync) return;
  try {
    authorized();
    await sync.flush();
    emit('error', '');
    if (!alive) {
      retained.delete(identity);
      unregister();
      window.removeEventListener('beforeunload', beforeUnload);
    }
  } catch (e) {
    fail(e);
    throw e;
  }
}
const unregister = registerDraftVisit(
  {
    identity: () => identity,
    dirty: () => !!state && sendableUpdates(state).length > 0,
    discard: () => {},
    flush,
    scope,
  },
  true,
);
class Caret extends WidgetType {
  constructor(private name: string) {
    super();
  }
  eq(other: Caret) {
    return other.name === this.name;
  }
  toDOM() {
    const el = document.createElement('span');
    el.className = 'cm-ySelectionCaret';
    el.style.cssText = 'background-color:#7f5af0;border-color:#7f5af0';
    el.append('\u2060');
    const dot = document.createElement('div');
    dot.className = 'cm-ySelectionCaretDot';
    el.append(dot, '\u2060');
    const label = document.createElement('div');
    label.className = 'cm-ySelectionInfo';
    label.textContent = this.name;
    el.append(label, '\u2060');
    return el;
  }
}
const setPeople = StateEffect.define<Presence[]>();
const cursors = StateField.define({
  create: () => Decoration.none,
  update(value, tr) {
    value = value.map(tr.changes);
    for (const effect of tr.effects)
      if (effect.is(setPeople)) {
        const ranges: Range<Decoration>[] = [];
        for (const p of effect.value) {
          if (p.user_id === person) continue;
          const head = Math.max(0, Math.min(p.head, tr.state.doc.length));
          ranges.push(Decoration.widget({ widget: new Caret(p.name), side: 1 }).range(head));
          const from = Math.max(0, Math.min(p.anchor, head)),
            to = Math.min(tr.state.doc.length, Math.max(p.anchor, head));
          if (from < to)
            ranges.push(
              Decoration.mark({
                class: 'cm-ySelection',
                attributes: { style: 'background-color:#7f5af033' },
              }).range(from, to),
            );
        }
        return Decoration.set(ranges, true);
      }
    return value;
  },
  provide: (f) => EditorView.decorations.from(f),
});
const cursorTheme = EditorView.baseTheme({
  '.cm-ySelectionCaret': {
    position: 'relative',
    borderLeft: '1px solid black',
    borderRight: '1px solid black',
    marginLeft: '-1px',
    marginRight: '-1px',
    boxSizing: 'border-box',
    display: 'inline',
  },
  '.cm-ySelectionCaretDot': {
    borderRadius: '50%',
    position: 'absolute',
    width: '.4em',
    height: '.4em',
    top: '-.2em',
    left: '-.2em',
    backgroundColor: 'inherit',
    transition: 'transform .3s ease-in-out',
    boxSizing: 'border-box',
  },
  '.cm-ySelectionCaret:hover > .cm-ySelectionCaretDot': {
    transformOrigin: 'bottom center',
    transform: 'scale(0)',
  },
  '.cm-ySelectionInfo': {
    position: 'absolute',
    top: '-1.05em',
    left: '-1px',
    fontSize: '.75em',
    fontFamily: 'serif',
    fontStyle: 'normal',
    fontWeight: 'normal',
    lineHeight: 'normal',
    userSelect: 'none',
    color: 'white',
    paddingLeft: '2px',
    paddingRight: '2px',
    zIndex: '101',
    transition: 'opacity .3s ease-in-out',
    backgroundColor: 'inherit',
    opacity: '0',
    transitionDelay: '0s',
    whiteSpace: 'nowrap',
  },
  '.cm-ySelectionCaret:hover > .cm-ySelectionInfo': { opacity: '1', transitionDelay: '0s' },
});
function target(): SelectionTarget | undefined {
  if (!view) return;
  const { from, to } = view.state.selection.main;
  if (from === to) return;
  const selected = window.getSelection();
  const bounds =
    selected && selected.rangeCount > 0
      ? selected.getRangeAt(0).getBoundingClientRect()
      : undefined;
  return {
    from,
    to,
    quote: view.state.sliceDoc(from, to),
    path: props.path,
    line: view.state.doc.lineAt(from).number,
    rect: {
      top: bounds?.top ?? 0,
      left: bounds?.left ?? 0,
      width: bounds?.width ?? 0,
      height: bounds?.height ?? 0,
    },
  };
}
async function selection() {
  await flush();
  const at = target();
  if (!at || !state) throw new Error('Select a passage in the document');
  return { ...at, file_id: file, version: getSyncedVersion(state) };
}
function jump(line: number) {
  if (!view) return;
  const at = view.state.doc.line(Math.max(1, Math.min(line, view.state.doc.lines))).from;
  view.dispatch({
    selection: { anchor: at },
    effects: EditorView.scrollIntoView(at, { y: 'center' }),
  });
  view.focus();
}
function searchFile() {
  if (view) {
    openSearchPanel(view);
    view.focus();
  }
}
async function settle(id: string, verdict: 'keep' | 'undo') {
  try {
    await flush();
    await api(
      'POST',
      `${projectPath(project)}/changes/${encodeURIComponent(id)}/${verdict}`,
      undefined,
      undefined,
      options,
    );
    await refresh();
  } catch (e) {
    fail(e);
  }
}
function queueCursor() {
  if (cursorTimer || props.disabled) return;
  cursorTimer = setTimeout(
    async () => {
      cursorTimer = undefined;
      if (!alive || !state) return;
      try {
        authorized();
        if (sendableUpdates(state).length) {
          lastCursor = Date.now();
          queueCursor();
          return;
        }
        lastCursor = Date.now();
        const { anchor, head } = state.selection.main;
        await api('POST', `${docPath(project, file)}/cursor`, { anchor, head }, undefined, options);
      } catch (e) {
        fail(e);
      }
    },
    Math.max(0, 500 - (Date.now() - lastCursor)),
  );
}
async function refresh() {
  if (refreshing || !alive || !sync || !view) return;
  refreshing = true;
  try {
    authorized();
    await sync.pull();
    const before = await api<Doc>('GET', docPath(project, file), undefined, undefined, options);
    const rows = await api<Change[]>(
      'GET',
      `${docPath(project, file)}/changes`,
      undefined,
      undefined,
      options,
    );
    const after = await api<Doc>('GET', docPath(project, file), undefined, undefined, options);
    if (!alive || !state || !view) return;
    if (before.version === after.version && after.version === getSyncedVersion(state)) {
      let mapped = (rows ?? []).filter((r) => r.status === 'pending' || r.status === 'stale');
      for (const update of sendableUpdates(state)) mapped = mapChangeRows(mapped, update.changes);
      view.dispatch({ effects: setChangeRows.of(mapped) });
    }
    const people = await api<Presence[]>(
      'GET',
      `${docPath(project, file)}/presence`,
      undefined,
      undefined,
      options,
    );
    if (alive && view && state) {
      const pending = sendableUpdates(state);
      view.dispatch({
        effects: setPeople.of(
          (people ?? []).map((p) => {
            let { anchor, head } = p;
            for (const u of pending) {
              anchor = u.changes.mapPos(anchor);
              head = u.changes.mapPos(head);
            }
            return { ...p, anchor, head };
          }),
        ),
      });
    }
  } catch (e) {
    fail(e);
  } finally {
    refreshing = false;
  }
}
watch(
  () => props.disabled,
  (value) =>
    view?.dispatch({
      effects: readOnly.reconfigure([
        EditorView.editable.of(!value),
        EditorState.readOnly.of(!!value),
      ]),
    }),
);
function beforeUnload(e: BeforeUnloadEvent) {
  if (state && sendableUpdates(state).length) {
    e.preventDefault();
    e.returnValue = '';
  }
}
onMounted(async () => {
  window.addEventListener('beforeunload', beforeUnload);
  try {
    const held = retained.get(identity);
    held?.unregister();
    held?.release();
    retained.delete(identity);
    const initial = held
      ? undefined
      : await api<Doc>('GET', docPath(project, file), undefined, undefined, options);
    if (!alive) return;
    const extensions = [
      lineNumbers(),
      errorLines,
      errorGutter,
      highlightActiveLine(),
      drawSelection(),
      indentOnInput(),
      StreamLanguage.define(stex),
      syntaxHighlighting(latexHighlight),
      search(),
      autocompletion({
        override: [
          latexCompletions({
            bibKeys: () => props.bibKeys ?? [],
            libraryRows: () => [],
            labels: () => props.labels ?? [],
            onAppendBib: () => {},
          }),
        ],
      }),
      EditorView.lineWrapping,
      history(),
      keymap.of([
        {
          key: 'Mod-Enter',
          run: () => {
            emit('build');
            return true;
          },
        },
        ...defaultKeymap,
        ...historyKeymap,
        ...searchKeymap,
        ...completionKeymap,
      ]),
      readOnly.of([
        EditorView.editable.of(!props.disabled),
        EditorState.readOnly.of(!!props.disabled),
      ]),
      trackedChanges({ onSettle: settle }),
      cursors,
      cursorTheme,
      EditorView.domEventHandlers({
        mouseup: () => {
          emit('selection', target());
          return false;
        },
      }),
      EditorView.updateListener.of((update) => {
        state = update.state;
        if (update.docChanged) {
          emit('text', state.doc.toString());
          sync?.schedule();
        }
        if (update.selectionSet || update.docChanged) {
          emit('cursor', state.doc.lineAt(state.selection.main.head).number);
          queueCursor();
        }
      }),
    ];
    state = held
      ? held.state.update({
          effects: StateEffect.reconfigure.of([
            EditorState.lineSeparator.of('\n'),
            collab({
              clientID: getClientID(held.state),
              startVersion: getSyncedVersion(held.state),
            }),
            ...extensions,
          ]),
        }).state
      : collaborativeState(initial!, crypto.randomUUID(), extensions);
    view = new EditorView({ state, parent: host.value });
    sync = createCollaboration({
      clientID: getClientID(state),
      state: () => state!,
      dispatch: (tr) => {
        state = tr.state;
        if (view) view.update([tr]);
        else if (retained.has(identity))
          retained.set(identity, {
            state,
            unregister,
            release: () => window.removeEventListener('beforeunload', beforeUnload),
          });
      },
      transport: {
        push: (base, client_id, steps) => {
          authorized();
          return api(
            'POST',
            `${docPath(project, file)}/steps`,
            { base, client_id, steps },
            undefined,
            options,
          );
        },
        pull: (after) => {
          authorized();
          return api(
            'GET',
            `${docPath(project, file)}/steps?after=${after}`,
            undefined,
            undefined,
            options,
          );
        },
      },
      onError: fail,
    });
    updateErrors();
    emit('ready');
    emit('text', state.doc.toString());
    void refresh();
    interval = setInterval(() => {
      void refresh();
      queueCursor();
    }, 1000);
    if (sendableUpdates(state).length) sync.schedule();
  } catch (e) {
    fail(e);
  }
});
onBeforeUnmount(() => {
  alive = false;
  clearInterval(interval);
  clearTimeout(cursorTimer);
  sync?.stop();
  if (state && sendableUpdates(state).length)
    retained.set(identity, {
      state,
      unregister,
      release: () => window.removeEventListener('beforeunload', beforeUnload),
    });
  else {
    unregister();
    window.removeEventListener('beforeunload', beforeUnload);
  }
  view?.destroy();
  view = undefined;
});
defineExpose({
  flush,
  undo: () => {
    if (view && !props.disabled) undo(view);
  },
  redo: () => {
    if (view && !props.disabled) redo(view);
  },
  selection,
  jump,
  search: searchFile,
  refresh,
  dirty: () => !!state && sendableUpdates(state).length > 0,
});
</script>
<template>
  <div class="manuscript-editor-shell"><div ref="host" class="manuscript-editor" /></div>
</template>
