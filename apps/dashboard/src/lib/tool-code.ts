import type { HighlighterCore } from 'shiki/core';
let highlighter: Promise<HighlighterCore> | undefined;
const languages = {
  python: () => import('shiki/langs/python.mjs'),
  r: () => import('shiki/langs/r.mjs'),
  bash: () => import('shiki/langs/bash.mjs'),
  javascript: () => import('shiki/langs/javascript.mjs'),
  typescript: () => import('shiki/langs/typescript.mjs'),
  json: () => import('shiki/langs/json.mjs'),
  sql: () => import('shiki/langs/sql.mjs'),
  julia: () => import('shiki/langs/julia.mjs'),
};
const loaded = new Map<string, Promise<void>>();
export async function highlightToolCode(code: string, language: string) {
  const lang = language.toLowerCase();
  const grammar = languages[lang as keyof typeof languages];
  if (!grammar) return;
  highlighter ??= Promise.all([import('shiki/core'), import('shiki/engine/javascript')]).then(
    ([core, engine]) =>
      core.createHighlighterCore({
        themes: [import('shiki/themes/github-dark.mjs')],
        langs: [],
        engine: engine.createJavaScriptRegexEngine(),
      }),
  );
  const core = await highlighter;
  if (!loaded.has(lang))
    loaded.set(
      lang,
      grammar().then((g) => core.loadLanguage(g)),
    );
  await loaded.get(lang);
  return core.codeToTokens(code, { lang, theme: 'github-dark' }).tokens;
}
