import type { HypothesisMeasure } from '../api/types';

export function objectFields(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;
}

export function readMeasure(value: unknown): HypothesisMeasure | undefined {
  const fields = objectFields(value);
  if (!fields) return;
  const { id, name, unit } = fields;
  if (
    typeof id === 'string' &&
    /^[a-z][a-z0-9_-]{0,63}$/.test(id) &&
    typeof name === 'string' &&
    name.length > 0 &&
    name === name.trim() &&
    [...name].length <= 120 &&
    typeof unit === 'string' &&
    unit.length > 0 &&
    unit === unit.trim() &&
    [...unit].length <= 64
  )
    return { id, name, unit };
}

export function measurementText(rawMeasure: unknown, value: unknown): string | undefined {
  const measure = readMeasure(rawMeasure);
  if (measure && typeof value === 'string' && value.trim())
    return `${measure.name}: ${value} ${measure.unit}`;
}
