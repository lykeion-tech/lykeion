export class DesktopAuthGate {
  private generation = 0;
  private shuttingDown = false;
  private installing: Promise<boolean> | null = null;
  private recovery: (() => Promise<void>) | null = null;
  private readonly mutations = new Set<number>();

  begin(): number {
    if (this.shuttingDown || this.mutations.size || this.installing || this.recovery)
      throw new Error('Another sign-in is in progress');
    return ++this.generation;
  }
  cancel(): void {
    ++this.generation;
  }
  async settle(): Promise<void> {
    await this.installing?.catch(() => {});
    if (this.recovery) {
      await this.recovery();
      this.recovery = null;
    }
  }
  async shutdown(stopDaemon: () => Promise<void>): Promise<PromiseSettledResult<void>[]> {
    this.shuttingDown = true;
    this.cancel();
    return Promise.allSettled([this.settle(), Promise.resolve().then(stopDaemon)]);
  }
  mutation(id = 0): Promise<void> {
    this.cancel();
    this.mutations.add(id);
    return (this.installing?.catch(() => {}) ?? Promise.resolve()).then(() => {
      if (this.recovery) throw new Error('Authentication recovery required');
    });
  }
  finishMutation(id: number): void {
    this.mutations.delete(id);
  }
  async commit(
    generation: number,
    install: () => Promise<void>,
    rollback: () => Promise<void> = async () => {},
  ): Promise<boolean> {
    if (this.installing) await this.installing.catch(() => {});
    if (this.recovery) throw new Error('Authentication recovery required');
    if (generation !== this.generation) return false;
    const pending = (async () => {
      try {
        await install();
      } catch (error) {
        try {
          await rollback();
        } catch (failure) {
          this.recovery = rollback;
          throw failure;
        }
        throw error;
      }
      if (generation === this.generation) return true;
      try {
        await rollback();
      } catch (failure) {
        this.recovery = rollback;
        throw failure;
      }
      return false;
    })();
    this.installing = pending;
    try {
      return await pending;
    } finally {
      if (this.installing === pending) this.installing = null;
    }
  }
}
