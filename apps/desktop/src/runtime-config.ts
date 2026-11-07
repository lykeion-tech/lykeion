import { readFile } from 'node:fs/promises';

function origin(value: unknown): string {
  if (typeof value !== 'string') throw new Error('Invalid desktop API origin');
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    throw new Error('Invalid desktop API origin');
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
    throw new Error('Invalid desktop API origin');
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
    if ((error as NodeJS.ErrnoException).code === 'ENOENT')
      return origin(typeof hostedOrigin === 'function' ? await hostedOrigin() : hostedOrigin);
    throw new Error('Cannot read desktop configuration');
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    throw new Error('Invalid desktop configuration');
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed))
    throw new Error('Invalid desktop configuration');
  const config = parsed as Record<string, unknown>;
  if (config.schemaVersion !== 1) throw new Error('Unsupported desktop configuration version');
  return origin(config.apiUrl);
}
