import type { DaemonPreferences, DaemonSession } from './daemon-types.js';
import { trustedDesktopFrame } from './renderer-origin.js';
export const daemonMethods = [
  'getSnapshot',
  'getPreferences',
  'setPreferences',
  'configureSession',
  'clearSession',
  'start',
  'stop',
  'restart',
] as const;
export type DaemonMethod = (typeof daemonMethods)[number];
export const previewOrigins = new Set([
  'http://127.0.0.1:1430',
  'http://localhost:1430',
  'http://127.0.0.1:1431',
  'http://localhost:1431',
]);
type Sender = { mainFrame: { url: string } };
export function validDaemonSender(
  event: { sender: unknown; senderFrame: unknown },
  current: Sender | null | undefined,
): boolean {
  if (!current || event.sender !== current || event.senderFrame !== current.mainFrame) return false;
  try {
    return previewOrigins.has(new URL(current.mainFrame.url).origin);
  } catch {
    return false;
  }
}
function record(value: unknown, keys: string[]): value is Record<string, unknown> {
  return (
    !!value &&
    typeof value === 'object' &&
    !Array.isArray(value) &&
    Object.keys(value).length === keys.length &&
    keys.every((key) => Object.hasOwn(value, key))
  );
}
export function validPreferences(value: unknown): value is DaemonPreferences {
  return (
    record(value, ['autoStart', 'autoStop']) &&
    typeof value.autoStart === 'boolean' &&
    typeof value.autoStop === 'boolean'
  );
}
export function validSession(value: unknown): value is DaemonSession {
  return (
    record(value, ['apiOrigin', 'userID', 'sessionToken']) &&
    typeof value.apiOrigin === 'string' &&
    value.apiOrigin.length < 2048 &&
    typeof value.userID === 'string' &&
    value.userID.length > 0 &&
    value.userID.length < 256 &&
    typeof value.sessionToken === 'string' &&
    value.sessionToken.length > 0 &&
    value.sessionToken.length < 16384 &&
    !/[\r\n]/.test(value.sessionToken)
  );
}
export function validateDaemonRequest(method: string, value: unknown): void {
  if (!daemonMethods.includes(method as DaemonMethod)) throw new Error('Unknown daemon operation');
  if (
    method === 'setPreferences'
      ? validPreferences(value)
      : method === 'configureSession'
        ? validSession(value)
        : value === undefined
  )
    return;
  throw new Error('Invalid daemon request');
}

import type { DaemonManager } from './daemon-manager.js';
type Event = { sender: unknown; senderFrame: unknown };
export function installDaemonIPC(
  ipc: { handle(channel: string, listener: (event: Event, value?: unknown) => unknown): void },
  current: () => Sender | null,
  manager: DaemonManager,
  bundledURL: string,
  desktopOrigin?: string,
): void {
  for (const method of daemonMethods) {
    ipc.handle(`lykeion:daemon:${method}`, (event, value) => {
      const sender = current();
      const readOnly = method === 'getSnapshot' || method === 'getPreferences';
      const bundledRead =
        readOnly &&
        sender &&
        event.sender === sender &&
        event.senderFrame === sender.mainFrame &&
        sender.mainFrame.url === bundledURL;
      const desktop = desktopOrigin && trustedDesktopFrame(event, sender, desktopOrigin);
      if (!(desktopOrigin ? desktop : validDaemonSender(event, sender) || bundledRead))
        throw new Error('Invalid sender');
      validateDaemonRequest(method, value);
      if (method === 'configureSession') return manager.configureSession(value as DaemonSession);
      if (method === 'setPreferences') return manager.setPreferences(value as DaemonPreferences);
      return manager[method]();
    });
  }
}
