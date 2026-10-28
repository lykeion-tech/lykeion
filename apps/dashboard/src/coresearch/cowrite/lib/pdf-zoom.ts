/**
 * The PDF viewer's zoom, as numbers: what "fit" is for a stage of a given
 * size, where the steps land, and where to scroll so zooming keeps the
 * reader's place.
 *
 * A zoom is a TRUE size — `1` is the page at its own size on screen, the
 * way a reader's "100%" means it — and "fit" is worked out from the stage.
 * Kept apart from the component because every one of these is arithmetic a
 * test can hold to a number, where the component's layout is something
 * jsdom never performs.
 */

/** CSS pixels to a PDF point: the page's "100%" on screen. */
export const PX_PER_PT = 96 / 72;

/** The sizes a reader can pick outright, and where the steps stop. */
export const PDF_ZOOM_PRESETS = [0.5, 0.75, 1, 1.5, 2, 3, 4] as const;

type Size = { width: number; height: number };

/**
 * The zoom that fits the page into the stage, less `margin` on every side:
 * the whole page (`"page"`), or its width alone for a stage that grows with
 * its content (`"width"`). A stage nothing has measured yet — jsdom, or the
 * render before layout — answers `1`.
 */
export function fitScale(stage: Size, page: Size, mode: "page" | "width", margin: number): number {
  const room = { width: stage.width - 2 * margin, height: stage.height - 2 * margin };
  if (room.width <= 0 || page.width <= 0) return 1;
  const byWidth = room.width / (page.width * PX_PER_PT);
  if (mode === "width" || room.height <= 0 || page.height <= 0) return byWidth;
  return Math.min(byWidth, room.height / (page.height * PX_PER_PT));
}

/** The next preset in `direction` from `zoom`, which need not be one. */
export function nextZoom(zoom: number, direction: -1 | 1): number {
  const presets: readonly number[] = PDF_ZOOM_PRESETS;
  const EPS = 1e-6;
  if (direction > 0) return presets.find((p) => p > zoom + EPS) ?? presets[presets.length - 1]!;
  return [...presets].reverse().find((p) => p < zoom - EPS) ?? presets[0]!;
}

/**
 * Where to scroll so the point that was at the stage's centre — held as a
 * fraction of the page (`centre`) — is at its centre again once the page is
 * `page` in size. Clamped at zero, for a page smaller than the stage.
 */
export function keepCentre(centre: { x: number; y: number }, page: Size, stage: Size): { left: number; top: number } {
  return {
    left: Math.max(0, page.width * centre.x - stage.width / 2),
    top: Math.max(0, page.height * centre.y - stage.height / 2),
  };
}
