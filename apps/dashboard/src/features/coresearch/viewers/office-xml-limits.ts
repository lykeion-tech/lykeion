const MAX_LENGTH = 1024 * 1024;
const MAX_DEPTH = 64;
const MAX_NODES = 10_000;
// Bound browser DOM parsing before handing untrusted markup to DOMParser.
export function safeOfficeXml(xml: string): boolean {
  if (xml.length > MAX_LENGTH) return false;
  let at = 0,
    depth = 0,
    nodes = 0;
  while (at < xml.length) {
    const open = xml.indexOf('<', at);
    if (open < 0) break;
    if (xml.startsWith('<!--', open)) {
      const end = xml.indexOf('-->', open + 4);
      if (end < 0) return false;
      at = end + 3;
      continue;
    }
    if (xml.startsWith('<![CDATA[', open)) {
      const end = xml.indexOf(']]>', open + 9);
      if (end < 0) return false;
      at = end + 3;
      continue;
    }
    if (xml.startsWith('<?', open)) {
      const end = xml.indexOf('?>', open + 2);
      if (end < 0) return false;
      at = end + 2;
      continue;
    }
    if (xml.startsWith('<!', open)) return false;
    let end = open + 1,
      quote = '';
    for (; end < xml.length; end++) {
      const char = xml[end]!;
      if (quote) {
        if (char === quote) quote = '';
      } else if (char === '"' || char === "'") quote = char;
      else if (char === '>') break;
      else if (char === '<') return false;
    }
    if (end === xml.length || quote) return false;
    if (xml[open + 1] === '/') {
      if (--depth < 0) return false;
    } else {
      if (++nodes > MAX_NODES) return false;
      if (!/\/\s*$/.test(xml.slice(open + 1, end)) && ++depth > MAX_DEPTH) return false;
    }
    at = end + 1;
  }
  return depth === 0;
}
