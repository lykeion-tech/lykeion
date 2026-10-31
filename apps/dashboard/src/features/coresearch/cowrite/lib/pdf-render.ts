import type { PDFDocumentProxy, PageViewport } from "pdfjs-dist";

/**
 * pdf.js, loaded only when a PDF is actually opened.
 *
 * The library and its worker are roughly a megabyte together, and a
 * researcher who opens no PDF should fetch neither. The module-level cache
 * means a grid of PDF cards pays for it once.
 */
let loading: Promise<typeof import("pdfjs-dist")> | null = null;

export function loadPdfjs(): Promise<typeof import("pdfjs-dist")> {
  loading ??= (async () => {
    const [pdfjs, worker] = await Promise.all([
      import("pdfjs-dist"),
      import("pdfjs-dist/build/pdf.worker.mjs?url"),
    ]);
    pdfjs.GlobalWorkerOptions.workerSrc = worker.default;
    return pdfjs;
  })();
  return loading;
}

/**
 * The target of a PDF link annotation, or ABSENT if it must not be one.
 *
 * Decision 3 of the spec accepts pdf.js building DOM from attacker-chosen
 * input on the app's own origin, on the condition that the dangerous part is
 * ours. This is that part.
 *
 * `new URL` is called with NO BASE deliberately. With one, `//evil/x` and
 * `/artifacts/…` would resolve against the lab's own origin and come back
 * looking like ordinary https links; without one they throw, and a PDF's
 * relative link has no meaning here anyway.
 */
export function linkHref(raw: unknown): string | undefined {
  if (typeof raw !== "string") return undefined;
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return undefined;
  }
  return url.protocol === "http:" || url.protocol === "https:" ? url.href : undefined;
}

export interface PageLink {
  href: string;
  /** Fractions of the rendered page, so the overlay scales with the canvas. */
  left: number;
  top: number;
  width: number;
  height: number;
}

/** An annotation's rectangle in viewport fractions. `PageViewport` converts
 *  POINTS, not rectangles — there is no `convertToViewportRectangle` in
 *  pdf.js 6 — so both corners are converted and normalised here. */
export function linkBox(rect: number[], viewport: PageViewport): Omit<PageLink, "href"> {
  const [x1, y1] = viewport.convertToViewportPoint(rect[0]!, rect[1]!);
  const [x2, y2] = viewport.convertToViewportPoint(rect[2]!, rect[3]!);
  const left = Math.min(x1 as number, x2 as number);
  const top = Math.min(y1 as number, y2 as number);
  return {
    left: left / viewport.width,
    top: top / viewport.height,
    width: Math.abs((x2 as number) - (x1 as number)) / viewport.width,
    height: Math.abs((y2 as number) - (y1 as number)) / viewport.height,
  };
}

/** Every http(s) link on a page, positioned. Everything else is dropped and
 *  its text stays text. */
export async function pageLinks(
  doc: PDFDocumentProxy,
  pageNumber: number,
  viewport: PageViewport,
): Promise<PageLink[]> {
  const page = await doc.getPage(pageNumber);
  const annotations = await page.getAnnotations();
  const links: PageLink[] = [];
  for (const annotation of annotations) {
    if (annotation.subtype !== "Link") continue;
    const href = linkHref(annotation.url);
    if (href === undefined) continue;
    links.push({ href, ...linkBox(annotation.rect, viewport) });
  }
  return links;
}
