export class DesktopAuthGate {
  private generation = 0;
  private installing: Promise<boolean> | null = null;
  private readonly mutations = new Set<number>();

  begin(): number {
    if (this.mutations.size || this.installing) throw new Error('Another sign-in is in progress');
    return ++this.generation;
  }
  cancel(): void {
    ++this.generation;
  }
  mutation(id = 0): Promise<void> {
    this.cancel();
    this.mutations.add(id);
    return (
      this.installing?.then(
        () => {},
        () => {},
      ) ?? Promise.resolve()
    );
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
    if (generation !== this.generation) return false;
    const pending = (async () => {
      try {
        await install();
      } catch (error) {
        await rollback();
        throw error;
      }
      if (generation === this.generation) return true;
      await rollback();
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
