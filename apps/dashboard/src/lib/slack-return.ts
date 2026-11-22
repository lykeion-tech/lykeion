import { readText, writeText } from '../storage';

const key = 'lykeion.slacklink';
// A signed-out Slack link keeps its workspace and token through sign-in (tab session only).
export function rememberSlackLink(path: string, fullPath: string) {
  if (path === '/slack/link') writeText(key, fullPath, 'session');
}
export function takeSlackLink(): string {
  const saved = readText(key, 'session');
  if (!saved?.startsWith('/slack/link?')) return '';
  writeText(key, '', 'session');
  return saved;
}
