import { describe, it, expect, vi } from 'vitest';
import { ChangeSet, EditorState, Text } from '@codemirror/state';
import { getSyncedVersion, sendableUpdates } from '@codemirror/collab';
import * as shared from './collab';

describe('shared CodeMirror journal', () => {
  it('converges two clients after interleaved UTF-16/CRLF edits and a forced 409', async () => {
    expect(shared.createCollaboration).toBeTypeOf('function');
    let doc = Text.of('a😀\r\nb'.split('\n'));
    const steps: { version: number; client_id: string; changes: unknown }[] = [];
    let conflicts = 0;
    const transport = {
      async push(base: number, client_id: string, changes: unknown[]) {
        if (base !== steps.length) {
          conflicts++;
          throw Object.assign(new Error('behind'), { status: 409 });
        }
        for (const raw of changes) {
          doc = ChangeSet.fromJSON(raw).apply(doc);
          steps.push({ version: steps.length + 1, client_id, changes: raw });
        }
        return { version: steps.length };
      },
      async pull(after: number) {
        return steps.slice(after, after + 500);
      },
    };
    function client(id: string) {
      let state = shared.collaborativeState({ version: 0, text: 'a😀\r\nb' }, id);
      const sync = shared.createCollaboration({
        clientID: id,
        state: () => state,
        dispatch: (tr) => {
          state = state.update(tr).state;
        },
        transport,
      });
      return {
        sync,
        get state() {
          return state;
        },
        edit(from: number, to: number, text: string) {
          state = state.update({ changes: { from, to, insert: Text.of(text.split('\n')) } }).state;
        },
      };
    }
    const a = client('a'),
      b = client('b');
    a.edit(1, 3, 'planet');
    b.edit(6, 6, '!\r\n😀');
    await a.sync.flush();
    await b.sync.flush();
    await a.sync.pull();
    expect(conflicts).toBe(1);
    expect(a.state.doc.toString()).toBe('aplanet\r\nb!\r\n😀');
    expect(b.state.doc.toString()).toBe(a.state.doc.toString());
    expect(doc.toString()).toBe(a.state.doc.toString());
    expect(sendableUpdates(a.state)).toHaveLength(0);
    expect(getSyncedVersion(b.state)).toBe(2);
  });
  it('pulls every 500-step page and preserves new typing while a push is in flight', async () => {
    expect(shared.createCollaboration).toBeTypeOf('function');
    let state = shared.collaborativeState({ version: 0, text: '' }, 'local');
    const history = Array.from({ length: 1001 }, (_, i) => ({
      version: i + 1,
      client_id: 'remote',
      changes: ChangeSet.of({ from: i, insert: 'x' }, i).toJSON(),
    }));
    const after: number[] = [];
    let release!: () => void;
    const sync = shared.createCollaboration({
      clientID: 'local',
      state: () => state,
      dispatch: (tr) => {
        state = state.update(tr).state;
      },
      transport: {
        async pull(n) {
          after.push(n);
          return history.slice(n, n + 500);
        },
        async push(base, id, changes) {
          if (base !== history.length) throw Object.assign(new Error('behind'), { status: 409 });
          if (!release)
            await new Promise<void>((r) => {
              release = r;
            });
          for (const raw of changes)
            history.push({ version: history.length + 1, client_id: id, changes: raw as never });
          return { version: history.length };
        },
      },
    });
    await sync.pull();
    expect(after).toEqual([0, 500, 1000]);
    state = state.update({ changes: { from: 1001, insert: 'first' } }).state;
    const saving = sync.flush();
    await vi.waitFor(() => expect(release).toBeTypeOf('function'));
    state = state.update({ changes: { from: 1006, insert: 'second' } }).state;
    release();
    await saving;
    expect(state.doc.toString()).toBe('x'.repeat(1001) + 'firstsecond');
    expect(sendableUpdates(state)).toHaveLength(0);
    expect(getSyncedVersion(state)).toBe(1003);
  });
  it('retains pending text after failure and batches at 300 milliseconds', async () => {
    vi.useFakeTimers();
    try {
      expect(shared.createCollaboration).toBeTypeOf('function');
      let state = shared.collaborativeState({ version: 0, text: 'before' }, 'local');
      const push = vi.fn(async () => {
        throw new Error('offline');
      });
      const error = vi.fn();
      const sync = shared.createCollaboration({
        clientID: 'local',
        state: () => state,
        dispatch: (tr) => {
          state = state.update(tr).state;
        },
        transport: { push, pull: async () => [] },
        onError: error,
      });
      state = state.update({ changes: { from: 6, insert: 'after' } }).state;
      sync.schedule();
      await vi.advanceTimersByTimeAsync(299);
      expect(push).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(1);
      expect(push).toHaveBeenCalledOnce();
      expect(state.doc.toString()).toBe('beforeafter');
      expect(sendableUpdates(state)).toHaveLength(1);
      expect(error).toHaveBeenCalled();
      sync.stop();
    } finally {
      vi.useRealTimers();
    }
  });
});

it('maps pending ranges through local edits and marks overlap stale', async () => {
  const { trackedChanges, changeRows, setChangeRows } = await import('./trackedChanges');
  expect(trackedChanges).toBeTypeOf('function');
  let state = EditorState.create({
    doc: 'start new end',
    extensions: [trackedChanges({ onSettle() {} })],
  });
  state = state.update({
    effects: setChangeRows.of([
      { id: 'h', from_pos: 6, to_pos: 9, removed_text: 'old', status: 'pending' } as never,
    ]),
  }).state;
  state = state.update({ changes: { from: 0, insert: 'prefix ' } }).state;
  expect(state.field(changeRows)[0]).toMatchObject({ from_pos: 13, to_pos: 16, status: 'pending' });
  state = state.update({ changes: { from: 14, to: 15, insert: 'X' } }).state;
  expect(state.field(changeRows)[0]).toMatchObject({ from_pos: 13, to_pos: 16, status: 'stale' });
});

it('keeps boundary insertions pending when ranges are refreshed with local edits', async () => {
  const { mapChangeRows } = await import('./trackedChanges');
  const row = { id: 'h', from_pos: 2, to_pos: 5, status: 'pending', removed_text: 'old' } as never;
  expect(mapChangeRows([row], ChangeSet.of({ from: 2, insert: 'X' }, 8))[0]).toMatchObject({
    from_pos: 3,
    to_pos: 6,
    status: 'pending',
  });
  expect(mapChangeRows([row], ChangeSet.of({ from: 5, insert: 'X' }, 8))[0]).toMatchObject({
    from_pos: 2,
    to_pos: 5,
    status: 'pending',
  });
  expect(mapChangeRows([row], ChangeSet.of({ from: 3, insert: 'X' }, 8))[0]!.status).toBe('stale');
});

it('acknowledges an accepted batch after its response is lost without duplicating text', async () => {
  let state = shared.collaborativeState({ version: 0, text: 'base' }, 'retry');
  const rows: { version: number; client_id: string; changes: unknown }[] = [];
  const sync = shared.createCollaboration({
    clientID: 'retry',
    state: () => state,
    dispatch: (tr) => {
      state = tr.state;
    },
    transport: {
      pull: async (after) => rows.slice(after),
      push: async (base, id, steps) => {
        if (base !== rows.length) throw Object.assign(new Error('behind'), { status: 409 });
        for (const changes of steps)
          rows.push({ version: rows.length + 1, client_id: id, changes });
        throw new Error('response lost');
      },
    },
  });
  state = state.update({ changes: { from: 4, insert: '😀' } }).state;
  await expect(sync.flush()).rejects.toThrow('response lost');
  expect(sendableUpdates(state)).toHaveLength(1);
  await sync.flush();
  expect(rows).toHaveLength(1);
  expect(state.doc.toString()).toBe('base😀');
  expect(sendableUpdates(state)).toHaveLength(0);
  expect(getSyncedVersion(state)).toBe(1);
});
