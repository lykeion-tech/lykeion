import { trustedDesktopFrame } from './renderer-origin.js';

export type DesktopUpdateCategory = 'unsupported' | 'unconfigured' | 'network' | 'invalid_metadata' | 'cache_full' | 'corrupt_state' | 'generic';
export type DesktopUpdateResult =
  | { state: 'current' | 'downloaded'; version: string }
  | { state: 'error'; category: DesktopUpdateCategory };

type UpdateEvent = { sender: unknown; senderFrame: unknown };
type UpdateSender = { mainFrame: { url: string } };

export function installDesktopUpdateIPC(
  ipc: { handle(name: string, handler: (event: UpdateEvent, value?: unknown) => unknown): void },
  current: () => UpdateSender | null,
  origin: string,
  document: () => string | null,
  update: DesktopUpdateCheck,
): void {
  const guard = (event: UpdateEvent, value: unknown) => {
    if (!trustedDesktopFrame(event, current(), origin)) throw new Error('Invalid sender');
    if (typeof value !== 'string' || value !== document())
      throw new Error('Invalid update request');
  };
  ipc.handle('lykeion:update:available', (event, value) => {
    guard(event, value);
    return update.available();
  });
  ipc.handle('lykeion:update:check', (event, value) => {
    guard(event, value);
    return update.check();
  });
  ipc.handle('lykeion:update:cancel', (event, value) => {
    guard(event, value);
    update.cancel();
  });
}

const stableVersion = /^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$/;

export class DesktopUpdateCheck {
  private active: AbortController | null = null;
  private completion: Promise<void> | null = null;
  private generation = 0;

  constructor(
    private readonly enabled: () => boolean,
    private readonly document: () => string | null,
    private readonly authenticationReady: () => boolean,
    private readonly verifyHelper: () => Promise<boolean>,
    private readonly runHelper: (signal: AbortSignal) => Promise<DesktopUpdateResult>,
  ) {}

  available(): boolean {
    try {
      return this.enabled() && this.document() !== null && this.authenticationReady();
    } catch {
      return false;
    }
  }

  cancel(): void {
    this.generation++;
    this.active?.abort();
  }

  async waitForStop(): Promise<void> {
    await this.completion;
  }

  async check(): Promise<DesktopUpdateResult> {
    const fail = () => new Error('Update check unavailable');
    if (!this.available() || this.active) throw fail();
    const document = this.document();
    const generation = this.generation;
    const controller = new AbortController();
    this.active = controller;
    let finish!: () => void;
    const completion = new Promise<void>((resolve) => { finish = resolve; });
    this.completion = completion;
    const current = () =>
      !controller.signal.aborted &&
      generation === this.generation &&
      document !== null &&
      this.document() === document &&
      this.available();
    try {
      if (!(await this.verifyHelper()) || !current()) throw fail();
      const result = await this.runHelper(controller.signal);
      if (!current() || !result || typeof result !== 'object') throw fail();
      if (result.state === 'error') {
        if (Object.keys(result).sort().join(',') !== 'category,state' ||
          !['unsupported', 'unconfigured', 'network', 'invalid_metadata', 'cache_full', 'corrupt_state', 'generic'].includes(result.category)) throw fail();
      } else if (
        Object.keys(result).sort().join(',') !== 'state,version' ||
        !['current', 'downloaded'].includes(result.state) ||
        typeof result.version !== 'string' || !stableVersion.test(result.version)
      )
        throw fail();
      return result;
    } catch {
      throw fail();
    } finally {
      if (this.active === controller) this.active = null;
      if (this.completion === completion) this.completion = null;
      finish();
    }
  }
}
