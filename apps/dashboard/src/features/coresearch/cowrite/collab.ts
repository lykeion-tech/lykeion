import { ChangeSet, EditorState, Text, type Extension, type Transaction } from '@codemirror/state';
import { collab, sendableUpdates, receiveUpdates, getSyncedVersion } from '@codemirror/collab';
import type { Doc, DocStep } from './types';

export const exactText = (text: string) => Text.of(text.split('\n'));
export function collaborativeState(doc: Doc, clientID: string, extensions: Extension[] = []) {
  return EditorState.create({
    doc: exactText(doc.text),
    extensions: [
      EditorState.lineSeparator.of('\n'),
      collab({ startVersion: doc.version, clientID }),
      ...extensions,
    ],
  });
}
export interface JournalTransport {
  push(base: number, clientID: string, changes: unknown[]): Promise<{ version: number }>;
  pull(after: number): Promise<DocStep[]>;
}
export function createCollaboration(options: {
  clientID: string;
  state: () => EditorState;
  dispatch: (transaction: Transaction) => void;
  transport: JournalTransport;
  onError?: (error: unknown) => void;
}) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let queue: Promise<void> = Promise.resolve();
  let stopped = false;
  const serial = (fn: () => Promise<void>) => {
    const next = queue.then(fn);
    queue = next.catch(() => {});
    return next;
  };
  async function receive() {
    for (;;) {
      const after = getSyncedVersion(options.state());
      const rows = await options.transport.pull(after);
      if (!rows.length) return;
      if (rows.some((row, i) => row.version !== after + i + 1))
        throw new Error('Noncontiguous document history');
      options.dispatch(
        receiveUpdates(
          options.state(),
          rows.map((row) => ({
            clientID: row.client_id,
            changes: ChangeSet.fromJSON(row.changes),
          })),
        ),
      );
      if (rows.length < 500) return;
    }
  }
  async function send() {
    for (;;) {
      const updates = sendableUpdates(options.state()).slice(0, 500);
      if (!updates.length) return;
      const base = getSyncedVersion(options.state());
      try {
        const result = await options.transport.push(
          base,
          options.clientID,
          updates.map((update) => update.changes.toJSON()),
        );
        if (result.version !== base + updates.length)
          throw new Error('Unexpected acknowledged document version');
        options.dispatch(receiveUpdates(options.state(), updates));
      } catch (error) {
        if ((error as { status?: number }).status !== 409) throw error;
        await receive();
        if (getSyncedVersion(options.state()) === base) throw error;
      }
    }
  }
  const flush = () => {
    clearTimeout(timer);
    timer = undefined;
    return serial(send);
  };
  return {
    flush,
    pull: () => serial(receive),
    // Safe to call on every tick: it only arms a push while text is unsent.
    schedule() {
      if (stopped || timer || !sendableUpdates(options.state()).length) return;
      timer = setTimeout(() => {
        timer = undefined;
        void flush().catch(options.onError ?? (() => {}));
      }, 300);
    },
    stop() {
      stopped = true;
      clearTimeout(timer);
      timer = undefined;
    },
  };
}
