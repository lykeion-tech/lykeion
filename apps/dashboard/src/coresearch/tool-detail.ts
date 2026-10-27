// Ported from Lykeion-L2 ToolStepDetail.tsx.
const EXT_LANG: Record<string, string> = {
  ts: 'ts',
  tsx: 'tsx',
  js: 'js',
  jsx: 'jsx',
  mjs: 'js',
  cjs: 'js',
  json: 'json',
  rs: 'rust',
  py: 'python',
  sh: 'bash',
  bash: 'bash',
  sql: 'sql',
  css: 'css',
  html: 'html',
  htm: 'html',
  md: 'markdown',
  markdown: 'markdown',
  diff: 'diff',
  patch: 'diff',
};

/** The highlighter language for a path, by its extension — or undefined when
 *  there is no path or the extension isn't one we bundle a grammar for. */
export function langForPath(path?: string): string | undefined {
  if (!path) return undefined;
  const m = /\.([a-z0-9]+)$/i.exec(path.trim());
  return m ? EXT_LANG[m[1]!.toLowerCase()] : undefined;
}

/**
 * Strip the `cat -n` line numbers off a `Read` result (`"  1\t…"`), so the file
 * contents can be highlighted as the file's own language. Defensive: only when
 * EVERY non-empty line has the number+tab shape does it strip — a file whose
 * real content doesn't match is left exactly as it came.
 */
export function stripReadLineNumbers(text: string): string {
  const numbered = /^\s*\d+\t/;
  const lines = text.split('\n');
  const nonEmpty = lines.filter((l) => l.length > 0);
  if (nonEmpty.length === 0 || !nonEmpty.every((l) => numbered.test(l))) {
    return text;
  }
  return lines.map((l) => l.replace(numbered, '')).join('\n');
}

/** One web result. `title` falls back to the URL when the source names none. */
export interface WebResult {
  title: string;
  url: string;
}

/**
 * Best-effort parse of a web search's result text into a title/url list. The
 * exact shape is the agent's, not ours, so this stays defensive and tries the
 * shapes a Claude web search actually produces, in order:
 *
 *  1. JSON (the completion's structured `raw_output`): an array, or an object
 *     with a `results`/`links`/`items`/`web_search_results` array of objects
 *     carrying a url + a title/name.
 *  2. Markdown links: `[title](https://…)`.
 *  3. A title line immediately followed by a bare URL line.
 *
 * Returns null when none match, so the caller can fall back to showing the raw
 * text rather than an empty list.
 */
export function parseWebResults(text: string): WebResult[] | null {
  const trimmed = text.trim();
  if (trimmed.startsWith('[') || trimmed.startsWith('{')) {
    try {
      const j: unknown = JSON.parse(trimmed);
      const arr = Array.isArray(j)
        ? j
        : ((j as Record<string, unknown>).results ??
          (j as Record<string, unknown>).links ??
          (j as Record<string, unknown>).items ??
          (j as Record<string, unknown>).web_search_results);
      if (Array.isArray(arr)) {
        const out = arr
          .filter((r): r is Record<string, unknown> => !!r && typeof r === 'object')
          .map((r) => {
            const url = String(r.url ?? r.link ?? r.href ?? '').trim();
            const title = String(r.title ?? r.name ?? url).trim();
            return { title, url };
          })
          .filter((r) => r.url.length > 0);
        if (out.length > 0) return out;
      }
    } catch {
      // Not JSON after all — fall through to the text shapes.
    }
  }
  const md = [...text.matchAll(/\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)/g)].map((m) => ({
    title: m[1]!.trim(),
    url: m[2]!.trim(),
  }));
  if (md.length > 0) return md;

  const lines = text.split('\n').map((l) => l.trim());
  const out: WebResult[] = [];
  const isUrl = (s: string) => /^https?:\/\//.test(s);
  for (let i = 0; i < lines.length; i++) {
    const here = lines[i]!;
    const next = lines[i + 1] ?? '';
    if (here.length > 0 && !isUrl(here) && isUrl(next)) {
      out.push({ title: here, url: next });
      i++;
    }
  }
  return out.length > 0 ? out : null;
}
