import { randomUUID } from 'node:crypto';

// A renderer obtains a one-use reservation before it dispatches authentication.
// It remains admitted across shutdown until forwarded or explicitly released.
export class DesktopAuthGate {
  private shuttingDown = false;
  private nextID = 0;
  private readonly mutations = new Set<number>();
  private readonly reservations = new Map<string, { id: number; path: string }>();
  private readonly drainWaiters = new Set<() => void>();
  private abandonedLogout = false;
  private recoveryEpoch = 0;
  private abandonedVerification = false;
  private recovery: Promise<void> | null = null;

  constructor(private readonly persistLogout: () => void = () => {}) {}

  reserve(path: string): string | null {
    if (
      this.shuttingDown ||
      this.abandonedLogout ||
      this.abandonedVerification ||
      !['/auth/verify-code', '/auth/logout'].includes(path)
    )
      return null;
    if (path === '/auth/logout') this.persistLogout();
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
  abandonUnclaimed(): void {
    for (const [token, entry] of this.reservations) {
      if (entry.path === '/auth/logout') {
        this.abandonedLogout = true;
        this.recoveryEpoch++;
      } else this.abandonedVerification = true;
      this.release(token);
    }
  }
  acknowledgeInterruptedVerification(): boolean {
    if (!this.abandonedVerification) return false;
    this.abandonedVerification = false;
    return true;
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
  networkBlocked(): boolean {
    return this.abandonedLogout;
  }
  networkEpoch(): number {
    return this.recoveryEpoch;
  }
  async networkReady(): Promise<void> {
    if (this.recovery) await this.withTimeout(this.recovery, 15000).catch(() => {});
    if (this.abandonedLogout) throw new Error('Logout recovery required');
  }
  private async withTimeout(pending: Promise<void>, timeoutMs: number): Promise<void> {
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      await Promise.race([
        pending,
        new Promise<void>((_, reject) => {
          timer = setTimeout(() => reject(new Error('Logout cleanup did not finish')), timeoutMs);
        }),
      ]);
    } finally {
      if (timer) clearTimeout(timer);
    }
  }
  private waitForDrain(timeoutMs: number): Promise<void> {
    return this.mutations.size
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
  }
  async recoverAbandonedLogout(clear: () => Promise<void>, timeoutMs = 15000): Promise<void> {
    if (!this.abandonedLogout) return;
    if (!this.recovery) {
      this.recovery = this.waitForDrain(timeoutMs)
        .then(clear)
        .then(() => {
          this.abandonedLogout = false;
        });
      const task = this.recovery;
      void task.then(
        () => {
          if (this.recovery === task) this.recovery = null;
        },
        () => {
          if (this.recovery === task) this.recovery = null;
        },
      );
    }
    await this.withTimeout(this.recovery, timeoutMs);
  }
  async shutdown(
    stopDaemon: () => Promise<void>,
    timeoutMs = 15000,
    clearAbandonedLogout: () => Promise<void> = async () => {
      throw new Error('Logout recovery unavailable');
    },
  ): Promise<PromiseSettledResult<void>[]> {
    this.shuttingDown = true;
    const drain = this.waitForDrain(timeoutMs);
    const finalize = drain.then(async () => {
      await this.recoverAbandonedLogout(clearAbandonedLogout, timeoutMs);
      if (this.abandonedVerification) {
        this.abandonedVerification = false;
        throw new Error('Sign-in was interrupted before it was sent');
      }
    });
    return Promise.allSettled([finalize, Promise.resolve().then(stopDaemon)]);
  }
}
