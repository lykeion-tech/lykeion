import type { ChildProcessWithoutNullStreams } from 'node:child_process';
import type { HeldStreamSession } from '../../apps/desktop/src/desktop-held-stream';

export function openHeldStream(child: ChildProcessWithoutNullStreams, signal: AbortSignal): Promise<HeldStreamSession>;
