---
name: literature-check
description: a claim must be checked against published literature because no evidence came with it
---
# Checking a claim against the literature

Through the door (`call_tool`): `{"server": "sources", "tool": "search_literature", "args": {"database": "openalex" |
"europepmc" | "arxiv", "query": "..."}}` and `{"server": "sources", "tool": "fetch_text", "args":
{"source_id": "..."}}`. Every call is recorded and
returns `call:<key>` to cite.

1. Query on the claim's specific entities and relation, a few short variants.
2. Fetch the most direct sources; prefer primary results and reviews over passing mentions.
3. For each source, quote the passage for or against the claim and record it:
   `lyk record put source --cites call:KEY --content '{"text": "<title> (<year>): <quote>"}'`
4. Verdict: holds when retrieved sources support it and none credibly contradict; fails when
   they contradict it; cannot tell when nothing relevant was found or the sources conflict
   without resolution. Say which.
Never cite a source you did not retrieve; if a source would not open, say so.
