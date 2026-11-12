export class AuthenticationReload {
  private pending = false;
  private revision = 0;

  constructor(
    private readonly isCurrent: () => boolean,
    private readonly isQuitting: () => boolean,
    private readonly isSafe: () => boolean,
    private readonly reload: () => void,
    private readonly schedule: (callback: () => void) => void = setImmediate,
    private readonly onUnsafe: () => void = () => {},
  ) {}

  request(): void {
    this.pending = true;
    this.schedule(() => this.attempt());
  }

  resumeAfterQuit(drained: Promise<void>): void {
    const revision = ++this.revision;
    void drained.then(
      () => {
        if (revision === this.revision) this.attempt();
      },
      () => {},
    );
  }

  private attempt(): void {
    if (!this.pending) return;
    if (!this.isCurrent()) {
      this.pending = false;
      return;
    }
    if (this.isQuitting()) return;
    if (!this.isSafe()) {
      this.onUnsafe();
      return;
    }
    this.pending = false;
    this.reload();
  }
}
