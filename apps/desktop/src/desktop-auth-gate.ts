import { randomUUID } from 'node:crypto';

// A renderer obtains a one-use reservation before it dispatches authentication.
// It remains admitted across shutdown until forwarded or explicitly released.
export class DesktopAuthGate {
  private shuttingDown = false;
  private nextID = 0;
  private readonly mutations = new Set<number>();
  private readonly reservations = new Map<string, { id: number; path: string }>();
  private readonly drainWaiters = new Set<() => void>();

  reserve(path: string): string | null {
    if (this.shuttingDown || !['/auth/verify-code', '/auth/logout'].includes(path)) return null;
    const id = ++this.nextID;
    const token = randomUUID();
    this.mutations.add(id);
    this.reservations.set(token, { id, path });
    return token;
  }
  release(token: string): void {
    const entry = this.reservations.get(token);
    if (!entry) return;
    this.reservations.delete(token);
    this.finishMutation(entry.id);
  }
  continueMutation(id: number, token: string, path: string): boolean {
    const entry = this.reservations.get(token);
    if (!entry || entry.path !== path) return false;
    this.reservations.delete(token);
    this.mutations.add(id);
    this.finishMutation(entry.id);
    return true;
  }
  finishMutation(id: number): void {
    this.mutations.delete(id);
    if (!this.mutations.size) {
      for (const resolve of this.drainWaiters) resolve();
      this.drainWaiters.clear();
    }
  }
  async shutdown(
    stopDaemon: () => Promise<void>,
    timeoutMs = 15000,
  ): Promise<PromiseSettledResult<void>[]> {
    this.shuttingDown = true;
    const drain = this.mutations.size
      ? new Promise<void>((resolve, reject) => {
          const done = () => {
            clearTimeout(timer);
            this.drainWaiters.delete(done);
            resolve();
          };
          const timer = setTimeout(() => {
            this.drainWaiters.delete(done);
            reject(new Error('Authentication request did not finish'));
          }, timeoutMs);
          this.drainWaiters.add(done);
        })
      : Promise.resolve();
    return Promise.allSettled([drain, Promise.resolve().then(stopDaemon)]);
  }
}
