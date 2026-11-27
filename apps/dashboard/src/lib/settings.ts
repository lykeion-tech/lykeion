import type { CreateFields } from '../state/preferences';

export function validCreateFields(value: unknown): value is CreateFields {
  return (
    !!value &&
    typeof value === 'object' &&
    Array.isArray((value as { agent: unknown }).agent) &&
    Array.isArray((value as { manual: unknown }).manual)
  );
}

