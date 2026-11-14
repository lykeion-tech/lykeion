import type { CreateFields } from '../state/preferences';
import type { LabelDraft } from '../state/settings-drafts';

export function validCreateFields(value: unknown): value is CreateFields {
  return (
    !!value &&
    typeof value === 'object' &&
    Array.isArray((value as { agent: unknown }).agent) &&
    Array.isArray((value as { manual: unknown }).manual)
  );
}

// The normalised URL, or null for anything but a plain HTTPS address (no credentials,
// query or fragment).
export function repositoryUrl(text: string) {
  try {
    const url = new URL(text);
    return url.protocol !== 'https:' || url.username || url.password || url.search || url.hash
      ? null
      : url.href;
  } catch {
    return null;
  }
}

// One option per line, trimmed, blank lines and repeats dropped; only select types have options.
export function propertyOptions(type: string, text: string) {
  return ['Select', 'Multi-select'].includes(type)
    ? [
        ...new Set(
          text
            .split('\n')
            .map((item) => item.trim())
            .filter(Boolean),
        ),
      ]
    : [];
}

// Label names are unique per scope, ignoring case and surrounding space.
export function labelNameTaken(
  labels: LabelDraft[],
  name: string,
  scope: LabelDraft['scope'],
  exceptId: string,
) {
  return labels.some(
    (item) =>
      item.id !== exceptId &&
      item.scope === scope &&
      item.name.toLowerCase() === name.trim().toLowerCase(),
  );
}
