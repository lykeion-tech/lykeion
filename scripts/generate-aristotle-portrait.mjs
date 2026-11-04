import { readFileSync, writeFileSync } from 'node:fs';
const root = new URL('../apps/dashboard/public/brand/', import.meta.url);
const data = JSON.parse(readFileSync(new URL('aristotle-idle.json', root), 'utf8'));
// Keep the approved head artwork and its animated ancestor transforms.
// Exclude the body and neck while retaining the full beard and animated head transforms.
function withoutNeck(shape) {
  if (!shape.it) return shape;
  return { ...shape, it: shape.it.filter((child) => child.nm !== 'neck').map(withoutNeck) };
}
function portrait(shapes) {
  return shapes.flatMap((shape) => {
    if (shape.ty !== 'gr') return shape.ty === 'tr' ? [shape] : [];
    if (shape.nm === 'head-pose') return [withoutNeck(shape)];
    const children = portrait(shape.it ?? []);
    return children.some((child) => child.ty === 'gr') ? [{ ...shape, it: children }] : [];
  });
}
for (const layer of data.layers) layer.shapes = portrait(layer.shapes ?? []);
writeFileSync(new URL('aristotle-portrait.json', root), JSON.stringify(data));
