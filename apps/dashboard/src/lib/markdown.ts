import MarkdownIt from 'markdown-it';
import { cleanAssistantProse } from '../features/coresearch/assistant-paths';

const markdown = new MarkdownIt({ html: false, breaks: true, linkify: true, typographer: false });
const escape = markdown.utils.escapeHtml;
const colleaguePath = (url: string) =>
  /^\/colleagues\/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(url);
// Only explicit web/mail destinations and document anchors are interactive.
markdown.validateLink = (url) =>
  /^(?:https?:\/\/|mailto:|#)/i.test(url) &&
  ![...url].some((character) => character.charCodeAt(0) <= 32 || character.charCodeAt(0) === 127);
markdown.renderer.rules.link_open = (tokens, index, options, _env, renderer) => {
  const token = tokens[index]!;
  const href = String(token.attrGet('href') ?? '');
  if (colleaguePath(href)) token.attrSet('href', `#${href}`);
  else {
    token.attrSet('target', '_blank');
    token.attrSet('rel', 'noopener noreferrer');
  }
  return renderer.renderToken(tokens, index, options);
};
markdown.renderer.rules.image = (tokens, index) =>
  `<span class="markdown-image">[Image: ${escape(tokens[index]!.content || 'recorded image')}]</span>`;
markdown.renderer.rules.table_open = () =>
  '<div class="markdown-table" tabindex="0" role="region" aria-label="Table"><table>\n';
markdown.renderer.rules.table_close = () => '</table></div>\n';
const code: NonNullable<typeof markdown.renderer.rules.fence> = (tokens, index, _options, env) => {
  const token = tokens[index]!;
  const codes = (env as { codes: string[] }).codes;
  const id = codes.push(token.content) - 1;
  const language = token.type === 'fence' ? token.info.trim().split(/\s+/)[0] : '';
  return `<div class="markdown-code"><div class="code-toolbar"><span>${escape(language || 'Code')}</span><button type="button" class="markdown-copy" aria-label="Copy code" data-copy-code="${id}"><svg class="copy-icon" width="12" height="12" viewBox="0 0 16 16" fill="none" aria-hidden="true"><g stroke="currentColor" stroke-width="1.2"><rect x="5.5" y="5.5" width="7" height="8" rx="1.5"/><path d="M10.5 5.5v-2A1 1 0 0 0 9.5 2.5h-6a1 1 0 0 0-1 1v7a1 1 0 0 0 1 1h2"/></g></svg><svg class="copied-icon" width="12" height="12" viewBox="0 0 16 16" fill="none" aria-hidden="true" hidden><path d="m3 8 3 3 7-7" stroke="currentColor" stroke-width="1.2"/></svg><span aria-live="polite">Copy</span></button></div><pre tabindex="0"><code data-language="${escape(language)}">${escape(token.content)}</code></pre></div>\n`;
};
markdown.renderer.rules.fence = code;
markdown.renderer.rules.code_block = code;

// Transcript-only parsing recognizes local paths so they can be shown without a link.
const transcriptMarkdown = new MarkdownIt(markdown.options);
const colleagueMarkdown = new MarkdownIt(markdown.options);
colleagueMarkdown.validateLink = (url) => markdown.validateLink(url) || colleaguePath(url);
for (const parser of [markdown, transcriptMarkdown, colleagueMarkdown]) {
  parser.renderer.rules = markdown.renderer.rules;
  parser.core.ruler.after('linkify', 'doi_links', (state) => {
    for (const token of state.tokens) {
      if (!token.children) continue;
      let linkDepth = 0;
      token.children = token.children.flatMap((child) => {
        if (child.type === 'link_open') {
          linkDepth++;
          return [child];
        }
        if (child.type === 'link_close') {
          linkDepth--;
          return [child];
        }
        if (child.type !== 'text' || linkDepth) return [child];
        const out = [];
        const pattern = /\b(?:doi:\s*)?(10\.\d{4,9}\/[-._;()/:a-z0-9]+)/gi;
        let offset = 0;
        for (const match of child.content.matchAll(pattern)) {
          let doi = match[1]!.replace(/[.,;:]+$/, '');
          while (
            doi.endsWith(')') &&
            (doi.match(/\)/g)?.length ?? 0) > (doi.match(/\(/g)?.length ?? 0)
          )
            doi = doi.slice(0, -1);
          const start = match.index!;
          const length = match[0].length - (match[1]!.length - doi.length);
          if (start > offset) {
            const text = new state.Token('text', '', 0);
            text.content = child.content.slice(offset, start);
            out.push(text);
          }
          const open = new state.Token('link_open', 'a', 1);
          open.attrs = [['href', `https://doi.org/${doi}`]];
          const text = new state.Token('text', '', 0);
          text.content = child.content.slice(start, start + length);
          const close = new state.Token('link_close', 'a', -1);
          out.push(open, text, close);
          offset = start + length;
        }
        if (!out.length) return [child];
        if (offset < child.content.length) {
          const text = new state.Token('text', '', 0);
          text.content = child.content.slice(offset);
          out.push(text);
        }
        return out;
      });
    }
  });
}
const localPath = (url: string) =>
  [
    '/private/',
    '/tmp/',
    '/var/',
    '/Users/',
    '/home/',
    '/workspace/',
    '/mnt/',
    '/opt/',
    '/data/',
    '/srv/',
    '/Volumes/',
  ].some((root) => url.startsWith(root));
transcriptMarkdown.validateLink = (url) => markdown.validateLink(url) || localPath(url);

export function renderMarkdown(content: string, breaks = true, localFiles = false) {
  const env = { codes: [] as string[] };
  // Raw HTML is disabled; all text, code and attributes are escaped by the parser.
  const parser = localFiles ? transcriptMarkdown : markdown;
  const tokens = parser.parse(content, env);
  if (localFiles) {
    for (const token of tokens) {
      const links: boolean[] = [];
      for (const child of token.children ?? []) {
        if (child.type === 'link_open') {
          const local = localPath(String(child.attrGet('href') ?? ''));
          links.push(local);
          if (local) {
            child.type = 'text';
            child.tag = '';
            child.nesting = 0;
            child.content = '';
          }
        } else if (child.type === 'link_close' && links.pop()) {
          child.type = 'text';
          child.tag = '';
          child.nesting = 0;
          child.content = ' (local file not available in Lykeion)';
        } else if (child.type === 'text' && !links.length) {
          child.content = cleanAssistantProse(child.content);
        }
      }
    }
  }
  return {
    html: parser.renderer.render(tokens, { ...parser.options, breaks }, env),
    codes: env.codes,
  };
}

// Block structure for component renderers. Inline HTML uses exactly the same
// escaping, URL validation, link and image rules as renderMarkdown above.
export function parseMarkdownBlocks(
  content: string,
  redactLocalPaths = false,
  colleagueLinks = false,
) {
  const parser = colleagueLinks ? colleagueMarkdown : markdown;
  const blocks = parser.parse(content, {});
  if (redactLocalPaths) {
    for (const token of blocks) {
      for (const child of token.children ?? []) {
        if (child.type === 'text') child.content = cleanAssistantProse(child.content);
        else if (
          child.type === 'code_inline' &&
          /^(?:file:\/\/)?\/(?:private|tmp|var|Users|home|workspace|mnt|opt|data|srv|Volumes)\/|^[A-Za-z]:\\/.test(
            child.content,
          )
        )
          child.content = '[local file]';
      }
    }
  }
  return blocks.map((token) => ({
    type: token.type,
    tag: token.tag,
    nesting: token.nesting,
    hidden: token.hidden,
    attrs: token.attrs,
    content: token.content,
    info: token.info,
    html:
      token.type === 'inline'
        ? parser.renderer.renderInline(token.children ?? [], parser.options, {})
        : undefined,
  }));
}
