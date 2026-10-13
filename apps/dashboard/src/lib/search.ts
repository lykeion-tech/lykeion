// Case-insensitive substring match; an empty query matches everything.
export function matchesQuery(text: string, query: string) {
  return text.toLowerCase().includes(query.toLowerCase());
}
