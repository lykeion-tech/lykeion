---
name: minimal-grounding
description: a hypothesis needs one missing fact looked up, without doing a literature review
---
# One narrow lookup

Use the sources server through the door (`call_tool`): `{"server": "sources", "tool": "search_literature", "args": {"database": "openalex" |
"europepmc" | "arxiv", "query": "..."}}`, then `{"server": "sources", "tool": "fetch_text", "args":
{"source_id": "..."}}` on the best hit. Every call is recorded and returns `call:<key>`.

- Query on the specific system and quantity (e.g. the material and property, the organism and
  phenotype), 3 to 8 words, two or three variants at most.
- Read only what the hypothesis needs. Record it:
  `lyk record put source --cites call:KEY --content '{"text": "<title> (<year>): <quoted passage>"}'`
- If nothing fits, `lyk record put limitation --content '{"text": "searched <queries>; nothing relevant"}'`.
- Never fill the gap from memory as if it were a source.
- Never look up this study's own answer, the result it is testing.
