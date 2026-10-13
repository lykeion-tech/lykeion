// Every Web Storage access goes through here. Storage can be unavailable (blocked site data,
// some private windows), full, or hold values another build wrote, so reads fall back and
// writes report success instead of throwing.

type Area = 'local' | 'session';

function area(name: Area): Storage {
  return name === 'local' ? globalThis.localStorage : globalThis.sessionStorage;
}

// Returns undefined when storage itself is unavailable, null when the key is unset.
export function readText(key: string, from: Area = 'local'): string | null | undefined {
  try {
    return area(from).getItem(key);
  } catch {
    return undefined;
  }
}

export function writeText(key: string, value: string, to: Area = 'local'): boolean {
  try {
    area(to).setItem(key, value);
    return true;
  } catch {
    return false;
  }
}

// A missing key, unreadable storage, bad JSON and a stored null all give the fallback.
export function readJSON<T>(key: string, fallback: T, from: Area = 'local'): T {
  try {
    return (JSON.parse(readText(key, from) || 'null') as T | null) ?? fallback;
  } catch {
    return fallback;
  }
}

export function writeJSON(key: string, value: unknown, to: Area = 'local'): boolean {
  return writeText(key, JSON.stringify(value), to);
}

// LocalResearchService tells unavailable storage from bad data itself, so it gets the raw
// calls; the store is looked up on each call rather than captured at construction.
export const localStore: Pick<Storage, 'getItem' | 'setItem'> = {
  getItem: (key) => area('local').getItem(key),
  setItem: (key, value) => area('local').setItem(key, value),
};
