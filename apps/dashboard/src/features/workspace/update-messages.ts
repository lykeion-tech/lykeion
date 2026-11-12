const messages: Readonly<Record<string, string>> = Object.freeze({
  unsupported: 'Update checks are not available for this platform or build yet.',
  unconfigured: 'This build has no configured update service. Check for a newer release from Lykeion.',
  network: 'The update service could not be reached. Check your connection and try again.',
  invalid_metadata: 'The update information could not be verified or has expired. Try again later.',
  cache_full: 'The update cache is full. Contact Lykeion support for help.',
  corrupt_state: 'The update cache needs attention. Contact Lykeion support for help.',
  generic: 'The update check could not be completed. Try again later.',
});

export function updateErrorMessage(category: string): string {
  return Object.hasOwn(messages, category) ? messages[category]! : messages.generic!;
}
