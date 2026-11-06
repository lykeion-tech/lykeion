interface ElementNode {
  name: string;
  attrs: Record<string, string>;
  children: ElementNode[];
  text: string;
}
interface Spectrum {
  id: string;
  peaks: { mz: number; intensity: number }[];
  tic: number;
  count: number;
  minMz: number | null;
  maxMz: number | null;
}
export interface MassXmlPreview {
  spectra: Spectrum[];
}
const MAX_TEXT = 2 * 1024 * 1024,
  MAX_PEAKS = 100000,
  MAX_SPECTRA = 200,
  MAX_NODES = 30000;
function parseXml(xml: string): ElementNode | undefined {
  if (xml.length > MAX_TEXT || /<!|&/.test(xml)) return undefined;
  xml = xml.replace(/^\s*<\?xml\s[^?]*\?>/, '');
  const root: ElementNode = { name: '', attrs: {}, children: [], text: '' },
    stack = [root];
  let pos = 0,
    count = 0;
  const tokens = /<[^>]*>|[^<]+/g;
  for (const match of xml.matchAll(tokens)) {
    if (match.index !== pos) return undefined;
    pos += match[0].length;
    const token = match[0];
    const parent = stack[stack.length - 1]!;
    if (!token.startsWith('<')) {
      if (parent.text.length + token.length > MAX_TEXT) return undefined;
      parent.text += token;
      continue;
    }
    if (token.startsWith('</')) {
      const name = token.match(/^<\/([\w:.-]+)\s*>$/)?.[1];
      if (!name || stack.length < 2 || stack.pop()!.name !== name) return undefined;
      continue;
    }
    const tag = token.match(/^<([\w:.-]+)((?:\s+[^>]*?)?)\s*(\/?)>$/);
    if (!tag || ++count > MAX_NODES || stack.length > 64) return undefined;
    const attrs: Record<string, string> = {};
    let rest = tag[2]!.trim();
    while (rest) {
      const attribute = rest.match(/^([\w:.-]+)\s*=\s*(["'])([^"']*)\2(?:\s+|$)/);
      if (!attribute || Object.hasOwn(attrs, attribute[1]!)) return undefined;
      attrs[attribute[1]!] = attribute[3]!;
      rest = rest.slice(attribute[0].length).trim();
    }
    const node: ElementNode = { name: tag[1]!, attrs, children: [], text: '' };
    parent.children.push(node);
    if (!tag[3]) stack.push(node);
  }
  if (pos !== xml.length || stack.length !== 1 || root.children.length !== 1 || root.text.trim())
    return undefined;
  return root.children[0];
}
function local(n: ElementNode) {
  return n.name.split(':').pop();
}
function descendants(n: ElementNode, name: string): ElementNode[] {
  const found: ElementNode[] = [];
  const walk = (x: ElementNode) => {
    for (const c of x.children) {
      if (local(c) === name) found.push(c);
      walk(c);
    }
  };
  walk(n);
  return found;
}
function count(s: string | undefined) {
  return s && /^\d+$/.test(s) && Number.isSafeInteger(Number(s)) ? Number(s) : undefined;
}
function decode64(text: string, maxBytes: number): Uint8Array | undefined {
  const s = text.replace(/\s/g, '');
  if (
    s.length > Math.ceil(maxBytes / 3) * 4 ||
    !/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(s)
  )
    return undefined;
  try {
    const raw = atob(s);
    if (raw.length > maxBytes) return undefined;
    return Uint8Array.from(raw, (c) => c.charCodeAt(0));
  } catch {
    return undefined;
  }
}
function values(
  data: Uint8Array,
  precision: number,
  little: boolean,
  length: number,
): number[] | undefined {
  const size = precision / 8;
  if (data.length !== size * length) return undefined;
  const view = new DataView(data.buffer, data.byteOffset, data.byteLength),
    result: number[] = [];
  for (let i = 0; i < length; i++) {
    const n =
      precision === 32 ? view.getFloat32(i * size, little) : view.getFloat64(i * size, little);
    if (!Number.isFinite(n)) return undefined;
    result.push(n);
  }
  return result;
}
function spectrum(id: string, mz: number[], intensity: number[]): Spectrum | undefined {
  if (
    id.length > 200 ||
    mz.length !== intensity.length ||
    mz.some((v, i) => v < 0 || (i > 0 && v < mz[i - 1]!)) ||
    intensity.some((v) => v < 0)
  )
    return undefined;
  let tic = 0;
  for (const n of intensity) {
    tic += n;
    if (!Number.isFinite(tic)) return undefined;
  }
  const peaks = mz.map((m, i) => ({ mz: m, intensity: intensity[i]! }));
  if (peaks.length <= 250)
    return { id, peaks, tic, count: peaks.length, minMz: mz[0] ?? null, maxMz: mz.at(-1) ?? null };
  const sampled = Array.from({ length: 250 }, (_, i) => {
    const start = Math.floor((i * peaks.length) / 250),
      end = Math.floor(((i + 1) * peaks.length) / 250);
    let best = peaks[start]!;
    for (let j = start + 1; j < end; j++)
      if (peaks[j]!.intensity > best.intensity) best = peaks[j]!;
    return best;
  });
  return {
    id,
    peaks: sampled,
    tic,
    count: peaks.length,
    minMz: mz[0] ?? null,
    maxMz: mz.at(-1) ?? null,
  };
}
export async function readMassXml(
  text: string,
  extension: string,
): Promise<MassXmlPreview | undefined> {
  const root = parseXml(text);
  if (!root || !['mzml', 'mzxml'].includes(extension)) return undefined;
  if (local(root)?.toLowerCase() !== extension) return undefined;
  const nodes = descendants(root, extension === 'mzml' ? 'spectrum' : 'scan');
  const containers = descendants(root, extension === 'mzml' ? 'spectrumList' : 'msRun');
  for (const container of containers) {
    const key = extension === 'mzml' ? 'count' : 'scanCount';
    if (
      container.attrs[key] !== undefined &&
      count(container.attrs[key]) !==
        descendants(container, extension === 'mzml' ? 'spectrum' : 'scan').length
    )
      return undefined;
  }
  if (
    !nodes.length ||
    nodes.length > MAX_SPECTRA ||
    descendants(root, 'referenceableParamGroupRef').length
  )
    return undefined;
  const spectra: Spectrum[] = [];
  for (const node of nodes) {
    const length = count(node.attrs[extension === 'mzml' ? 'defaultArrayLength' : 'peaksCount']);
    if (length === undefined || length > MAX_PEAKS) return undefined;
    let mz: number[] | undefined, intensity: number[] | undefined;
    if (extension === 'mzxml') {
      const peaks = node.children.filter((n) => local(n) === 'peaks');
      if (peaks.length !== 1) return undefined;
      const p = peaks[0]!,
        precision = Number(p.attrs.precision);
      if (
        p.children.length ||
        ![32, 64].includes(precision) ||
        !['network', 'big'].includes(p.attrs.byteOrder ?? '') ||
        !['none', ''].includes(p.attrs.compressionType ?? '') ||
        (p.attrs.pairOrder && p.attrs.pairOrder !== 'm/z-int')
      )
        return undefined;
      const decoded = decode64(p.text, ((length * precision) / 8) * 2);
      if (!decoded) return undefined;
      const paired = values(decoded, precision, false, length * 2);
      if (!paired) return undefined;
      mz = paired.filter((_, i) => i % 2 === 0);
      intensity = paired.filter((_, i) => i % 2 === 1);
    } else {
      const arrays = descendants(node, 'binaryDataArray');
      if (arrays.length !== 2) return undefined;
      for (const a of arrays) {
        const terms = descendants(a, 'cvParam');
        if (
          terms.some(
            (c) =>
              /compression|numpress/i.test(c.attrs.name ?? '') &&
              c.attrs.accession !== 'MS:1000576',
          )
        )
          return undefined;
        const cv = terms.map((c) => c.attrs.accession);
        const allowed = ['MS:1000514', 'MS:1000515', 'MS:1000521', 'MS:1000523', 'MS:1000576'];
        if (cv.some((x) => !x || !allowed.includes(x)) || new Set(cv).size !== cv.length)
          return undefined;
        if (
          cv.some((x) => ['MS:1000574', 'MS:1002312', 'MS:1002313', 'MS:1002314'].includes(x ?? ''))
        )
          return undefined;
        const precision =
          cv.includes('MS:1000521') && !cv.includes('MS:1000523')
            ? 32
            : cv.includes('MS:1000523') && !cv.includes('MS:1000521')
              ? 64
              : 0;
        const isMz = cv.includes('MS:1000514'),
          isIntensity = cv.includes('MS:1000515');
        const binaries = a.children.filter((x) => local(x) === 'binary');
        if (
          !precision ||
          !cv.includes('MS:1000576') ||
          isMz === isIntensity ||
          binaries.length !== 1 ||
          binaries[0]!.children.length > 0
        )
          return undefined;
        const decoded = decode64(binaries[0]!.text, (length * precision) / 8);
        if (!decoded) return undefined;
        const parsed = values(decoded, precision, true, length);
        if (!parsed) return undefined;
        if (isMz && !mz) mz = parsed;
        else if (isIntensity && !intensity) intensity = parsed;
        else return undefined;
      }
    }
    if (!mz || !intensity) return undefined;
    const parsed = spectrum(node.attrs.id ?? node.attrs.num ?? '', mz, intensity);
    if (!parsed) return undefined;
    spectra.push(parsed);
  }
  return { spectra };
}

export function spectrumPlot(peaks: { mz: number; intensity: number }[], min: number, max: number) {
  if (!peaks.length) return [];
  const magnitude = Math.max(...peaks.map((p) => p.intensity)) || 1;
  const scale = Math.max(1, Math.abs(min), Math.abs(max));
  const low = min / scale,
    range = max / scale - low || 1;
  return peaks.map((p) => ({
    x: 10 + ((p.mz / scale - low) / range) * 280,
    y: 90 - (p.intensity / magnitude) * 80,
  }));
}
