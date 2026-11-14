---
name: literature-search
description: a step needs scientific sources found, opened and recorded
---
# Finding and recording sources

Tools (through the door, `call_tool`):
- `{"server": "sources", "tool": "search_literature", "args": {"database": "openalex",
  "query": "..."}}` for any field, `"europepmc"` for life sciences and medicine, or `"arxiv"` for preprints in physics, maths, computer science and quantitative biology (its hits carry arXiv ids). Each call returns up to a page of hits with an
  id, title, DOI, year and a short abstract.
- `{"server": "sources", "tool": "fetch_text", "args": {"source_id": "<id from a hit>"}}` returns the full text when it is open,
  else the abstract. Only fetched sources may be quoted.
Every call is recorded; name each source by its identifiers.

Searching well:
- Turn the request into its distinct conditions first, then query on the rarest, most specific
  ones (a named method, dataset, metric, organism, material) before broad topic words.
- Run several short queries (3 to 8 words) rather than one long sentence; search engines match
  terms, not questions. Try synonyms, abbreviations spelled out and the reverse.
- A near-miss is a clue: its title words, its field's vocabulary and its year range suggest the
  next query.
- Stop a line of search when two queries in a row add nothing new.

Reporting a source (one entry per source you rely on): its title and year, its DOI or arXiv id,
and what it says that matters, quoted from the fetched text.

Identifiers:
- Copy identifiers exactly as the tool returned them; never complete or guess one.
- A DOI of the form 10.48550/arXiv.NNNN.NNNNN names an arXiv preprint: report it also as
  arxiv:NNNN.NNNNN (no version suffix). A source URL on arxiv.org/abs/ gives the same identifier.
- If a source has no identifier in what you retrieved, say so rather than inventing one.

If a tool call fails or a text will not open, say so in your answer and carry on with what you
have.
