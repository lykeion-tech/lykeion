// Keep an admitted email verification or logout alive through app shutdown,
// including the native request that forwards a renderer request.
export class DesktopAuthGate {
  private shuttingDown = false;
  private readonly mutations = new Set<number>();
  private readonly drainWaiters = new Set<() => void>();

  mutation(id: number): Promise<void> {
    if (this.shuttingDown) return Promise.reject(new Error('Application is shutting down'));
    this.mutations.add(id);
    return Promise.resolve();
  }
  finishMutation(id: number): void {
    this.mutations.delete(id);
    if (!this.mutations.size) {
      for (const resolve of this.drainWaiters) resolve();
      this.drainWaiters.clear();
    }
  }
  async shutdown(stopDaemon: () => Promise<void>): Promise<PromiseSettledResult<void>[]> {
    this.shuttingDown = true;
    const drain = this.mutations.size
      ? new Promise<void>((resolve) => this.drainWaiters.add(resolve))
      : Promise.resolve();
    return Promise.allSettled([drain, Promise.resolve().then(stopDaemon)]);
  }
}
