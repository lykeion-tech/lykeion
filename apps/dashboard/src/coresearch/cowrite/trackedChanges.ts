// Ported from Lykeion-L2 lib/tracked-changes.ts; ranges come from the journal.
import {
  StateEffect,
  StateField,
  type Extension,
  type Range,
  type ChangeSet,
} from '@codemirror/state';
import {
  Decoration,
  EditorView,
  GutterMarker,
  WidgetType,
  gutter,
  type DecorationSet,
} from '@codemirror/view';
import type { Change } from './types';
export const setChangeRows = StateEffect.define<Change[]>();
export const changeRows = StateField.define<Change[]>({
  create: () => [],
  update(value, tr) {
    for (const effect of tr.effects) if (effect.is(setChangeRows)) return effect.value;
    if (!tr.docChanged) return value;
    return mapChangeRows(value, tr.changes);
  },
});
export function mapChangeRows(rows: Change[], changes: ChangeSet): Change[] {
  return rows.map((row) => {
    let touched = false;
    changes.iterChangedRanges((from, to) => {
      if (
        (from < row.to_pos && to > row.from_pos) ||
        (from === to && from > row.from_pos && from < row.to_pos) ||
        row.file_action === 'remove'
      )
        touched = true;
    });
    const from = changes.mapPos(row.from_pos, 1),
      to = Math.max(from, changes.mapPos(row.to_pos, -1));
    return { ...row, from_pos: from, to_pos: to, status: touched ? 'stale' : row.status };
  });
}
class SettleWidget extends WidgetType {
  constructor(
    private id: string,
    private stale: boolean,
    private settle: (id: string, verdict: 'keep' | 'undo') => void,
  ) {
    super();
  }
  eq(other: SettleWidget) {
    return other.id === this.id && other.stale === this.stale;
  }
  toDOM() {
    const box = document.createElement('div');
    box.className = 'cm-settle';
    if (this.stale) {
      const note = document.createElement('span');
      note.className = 'cm-settle-stale';
      note.textContent = 'the text changed under this edit';
      box.append(note);
    }
    const undo = document.createElement('button');
    undo.type = 'button';
    undo.className = 'cm-settle-undo';
    undo.textContent = 'Undo';
    undo.onclick = () => this.settle(this.id, 'undo');
    box.append(undo);
    if (!this.stale) {
      const keep = document.createElement('button');
      keep.type = 'button';
      keep.className = 'cm-settle-keep';
      keep.textContent = 'Keep';
      keep.onclick = () => this.settle(this.id, 'keep');
      box.append(keep);
    }
    return box;
  }
  ignoreEvent() {
    return false;
  }
}
class RemovedText extends WidgetType {
  constructor(private text: string) {
    super();
  }
  eq(other: RemovedText) {
    return other.text === this.text;
  }
  toDOM() {
    const el = document.createElement(this.text.includes('\n') ? 'div' : 'span');
    el.className = this.text.includes('\n') ? 'cm-del-line' : 'cm-del';
    el.textContent = this.text;
    return el;
  }
}
class LineNo extends GutterMarker {
  constructor(
    private text: string,
    private cls: string,
  ) {
    super();
  }
  toDOM() {
    const el = document.createElement('span');
    el.className = this.cls;
    el.textContent = this.text;
    return el;
  }
}
export function trackedChanges(opts: {
  onSettle: (id: string, verdict: 'keep' | 'undo') => void;
}): Extension {
  const decorations = StateField.define<DecorationSet>({
    create: () => Decoration.none,
    update(_value, tr) {
      const items: Range<Decoration>[] = [];
      for (const row of tr.state.field(changeRows)) {
        const from = Math.max(0, Math.min(row.from_pos, tr.state.doc.length)),
          to = Math.max(from, Math.min(row.to_pos, tr.state.doc.length));
        const first = tr.state.doc.lineAt(from),
          last = tr.state.doc.lineAt(Math.max(from, to - 1));
        if (to > from) {
          const whole = from === first.from && (to === last.to || to === last.to + 1);
          if (whole) {
            for (let n = first.number; n <= last.number; n++)
              items.push(
                Decoration.line({ class: 'cm-ins-line' }).range(tr.state.doc.line(n).from),
              );
          } else items.push(Decoration.mark({ class: 'cm-ins' }).range(from, to));
        }
        if (row.removed_text)
          items.push(
            Decoration.widget({
              widget: new RemovedText(row.removed_text),
              side: 1,
              block: row.removed_text.includes('\n'),
            }).range(to),
          );
        items.push(
          Decoration.widget({
            widget: new SettleWidget(row.id, row.status === 'stale', opts.onSettle),
            block: true,
            side: 2,
          }).range(last.to),
        );
      }
      return Decoration.set(items, true);
    },
    provide: (field) => EditorView.decorations.from(field),
  });
  const numbers = gutter({
    class: 'cm-oldnew-gutter',
    lineMarker(view, line) {
      const row = view.state
        .field(changeRows)
        .find((r) => r.from_pos <= line.from && r.to_pos > line.from);
      return row
        ? new LineNo(`${view.state.doc.lineAt(line.from).number} +`, 'cm-gutter-new')
        : null;
    },
    lineMarkerChange: (u) => u.startState.field(changeRows) !== u.state.field(changeRows),
  });
  return [changeRows, decorations, numbers];
}
