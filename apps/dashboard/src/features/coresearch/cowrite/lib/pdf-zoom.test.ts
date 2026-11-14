import { expect, it } from "vitest";
import { PDF_ZOOM_PRESETS, PX_PER_PT, fitScale, keepCentre, nextZoom } from "./pdf-zoom";
import { rasterWidth } from "./pdf-render";

it("renders fitted pages at twice their on-screen resolution even below a device pixel ratio of one", () => {
  expect(rasterWidth(621, 0.9)).toBe(1242);
  expect(rasterWidth(621, 3)).toBe(1863);
  expect(rasterWidth(2000, 2)).toBe(2600);
});

const letter = { width: 612, height: 792 };

it("fits the whole page inside the stage, less its margin, when fitting the page", () => {
  // A tall stage: the width is what binds.
  expect(fitScale({ width: 832, height: 2000 }, letter, "page", 16)).toBeCloseTo(800 / (612 * PX_PER_PT));
  // A short stage: the height binds, so the whole page is on screen.
  expect(fitScale({ width: 832, height: 632 }, letter, "page", 16)).toBeCloseTo(600 / (792 * PX_PER_PT));
});

it("fits the width alone when asked to, whatever the height", () => {
  expect(fitScale({ width: 832, height: 100 }, letter, "width", 16)).toBeCloseTo(800 / (612 * PX_PER_PT));
});

it("answers the page's own size for a stage nothing has measured yet", () => {
  expect(fitScale({ width: 0, height: 0 }, letter, "page", 16)).toBe(1);
});

it("steps through the presets from wherever the zoom is, and stops at their ends", () => {
  expect(PDF_ZOOM_PRESETS).toEqual([0.5, 0.75, 1, 1.5, 2, 3, 4]);
  expect(nextZoom(1, 1)).toBe(1.5);
  expect(nextZoom(1, -1)).toBe(0.75);
  // From a fitted zoom between two presets, to the nearest one each way.
  expect(nextZoom(0.83, 1)).toBe(1);
  expect(nextZoom(0.83, -1)).toBe(0.75);
  expect(nextZoom(4, 1)).toBe(4);
  expect(nextZoom(0.5, -1)).toBe(0.5);
});

it("keeps the point at the stage's centre where it was when the page grows or shrinks", () => {
  // The centre was 40% across and 25% down a 1000×2000 page in a 400×500
  // stage; the page doubles, and the same point is at the centre again.
  const at = keepCentre({ x: 0.4, y: 0.25 }, { width: 2000, height: 4000 }, { width: 400, height: 500 });
  expect(at).toEqual({ left: 2000 * 0.4 - 200, top: 4000 * 0.25 - 250 });
  // Never a negative scroll, for a page smaller than the stage.
  expect(keepCentre({ x: 0.5, y: 0 }, { width: 300, height: 400 }, { width: 400, height: 500 })).toEqual({ left: 0, top: 0 });
});
