import { lstat, readFile, stat } from 'node:fs/promises';
import path from 'node:path';

type ConfigError = 'invalid' | 'unreadable' | 'release';
class DesktopConfigError extends Error {
  constructor(readonly kind: ConfigError) {
    super('Desktop configuration error');
  }
}

export function desktopConfigGuidance(error: unknown): string {
  if (error instanceof DesktopConfigError) {
    const configFile = '~/.lyk/desktop.json (Windows: %USERPROFILE%\\.lyk\\desktop.json)';
    const example = [
      'Example: { "schemaVersion": 1, "apiUrl": "https://your-server.example" }',
      'Use an HTTPS server origin. HTTP is allowed only for localhost, 127.0.0.1, or [::1].',
    ].join('\n');
    if (error.kind === 'invalid')
      return `Correct or remove ${configFile} and restart Lykeion.\n\n${example}`;
    if (error.kind === 'unreadable')
      return `Check that ${configFile} and its directory can be read, then restart Lykeion.`;
    return `The hosted server address is missing or invalid in this release. Reinstall Lykeion or configure ${configFile}, then restart Lykeion.\n\n${example}`;
  }
  return 'Unable to load the desktop server configuration. Restart Lykeion or reinstall the app.';
}

async function configIsAbsent(file: string): Promise<boolean> {
  let current = file;
  for (;;) {
    try {
      const info = await lstat(current);
      if (current === file) return false;
      if (info.isSymbolicLink()) {
        try {
          await stat(current);
        } catch (error) {
          if ((error as NodeJS.ErrnoException).code === 'ENOENT') return false;
          throw error;
        }
      }
      return true;
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error;
      const parent = path.dirname(current);
      if (parent === current) throw error;
      current = parent;
    }
  }
}

function origin(value: unknown): string {
  if (typeof value !== 'string' || !/^https?:\/\/[^/?#\\]+\/?$/i.test(value))
    throw new DesktopConfigError('invalid');
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    throw new DesktopConfigError('invalid');
  }
  if (
    (url.protocol !== 'https:' &&
      !(
        url.protocol === 'http:' &&
        /^http:\/\/(localhost|127\.0\.0\.1|\[::1\])(?::\d+)?\/?$/i.test(value)
      )) ||
    url.username ||
    url.password ||
    url.pathname !== '/' ||
    url.search ||
    url.hash
  )
    throw new DesktopConfigError('invalid');
  return url.origin;
}

export async function loadDesktopConfig(
  file: string,
  hostedOrigin: string | undefined | (() => Promise<string | undefined>),
): Promise<string> {
  let raw: string;
  try {
    raw = await readFile(file, 'utf8');
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') {
      let absent: boolean;
      try {
        absent = await configIsAbsent(file);
      } catch {
        throw new DesktopConfigError('unreadable');
      }
      if (absent) {
        try {
          return origin(typeof hostedOrigin === 'function' ? await hostedOrigin() : hostedOrigin);
        } catch {
          throw new DesktopConfigError('release');
        }
      }
    }
    throw new DesktopConfigError('unreadable');
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    throw new DesktopConfigError('invalid');
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed))
    throw new DesktopConfigError('invalid');
  const config = parsed as Record<string, unknown>;
  if (config.schemaVersion !== 1) throw new DesktopConfigError('invalid');
  return origin(config.apiUrl);
}
