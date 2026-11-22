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
// Whether navigation reached target; a target may carry a query that route.path never has.
export const landedOn = (route: { path: string }, target: string) =>
  route.path === target.split('?')[0];
