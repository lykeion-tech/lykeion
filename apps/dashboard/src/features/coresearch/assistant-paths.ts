// Display cleanup for agent prose, not a privacy or security boundary.
const roots = String.raw`(?:file:\/\/)?\/(?:private|tmp|var|Users|home|workspace|mnt|opt|data|srv|Volumes)\/|[A-Za-z]:\\`;
const localLink = new RegExp(String.raw`\[([^\]\n]+)\]\(((?:${roots})[^)\n]*)\)`, 'g');
// Unquoted paths end at whitespace: prose following a path may itself contain
// filenames, and swallowing it would erase the cause of a failure.
const quotedPath = new RegExp(String.raw`(["'])(?:${roots})[^"'\n]*\1`, 'g');
const path = new RegExp(String.raw`(^|[\s("'\x60])(?:${roots})[^\s<>"'\x60()\[\]]+`, 'g');
export function cleanAssistantProse(text: string): string {
  return text.replace(localLink, '$1 [local file]').replace(quotedPath, '$1[local file]$1').replace(path, '$1[local file]');
}
export function cleanAssistantText(text: string): string {
  let fence = '';
  return text.split(/(\n)/).map((line) => {
    const marker = /^\s{0,3}(`{3,}|~{3,})/.exec(line)?.[1];
    if (marker && (!fence || (marker[0] === fence[0] && marker.length >= fence.length))) {
      fence = fence ? '' : marker;
      return line;
    }
    if (fence || line === '\n') return line;
    // Preserve web URLs and useful inline commands; path-only inline code is
    // a label rather than executable guidance and can be replaced safely.
    return line.split(/(https?:\/\/[^\s<>]+|`[^`\n]*`)/g).map((part) => {
      if (/^https?:\/\//.test(part)) return part;
      if (part.startsWith('`') && part.endsWith('`')) {
        const value = part.slice(1, -1);
        return new RegExp(String.raw`^(?:${roots})`).test(value) ? '`[local file]`' : part;
      }
      return cleanAssistantProse(part);
    }).join('');
  }).join('');
}
