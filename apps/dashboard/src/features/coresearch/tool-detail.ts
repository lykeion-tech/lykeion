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
