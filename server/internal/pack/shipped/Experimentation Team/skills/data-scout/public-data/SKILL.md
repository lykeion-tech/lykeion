---
name: public-data
description: the study needs data it does not hold and public data may fit
---
# Finding public data

Through the door (`call_tool`): `{"server": "sources", "tool": "search_datasets", "args": {"query": "..."}}` and
`{"server": "sources", "tool": "open_dataset", "args": {"accession": "GSE..."}}` (GEO). Literature through `search_literature` and `fetch_text`
can point to datasets a paper deposited. Every call is recorded and returns `call:<key>`.

- Search on the organism or system, the measurement type and the condition.
- Before proposing a dataset, check from its metadata that it measures what the plan needs, on
  enough samples, with the groups the comparison needs.
- Record it: `lyk record put dataset --cites call:KEY --content '{"text": "<accession>: <what it holds, why it fits>"}'`.
- Metadata is not the data: say what still has to be downloaded and whether that was possible.
- If nothing fits, say so; never make up or simulate data and present it as real.
