# Graphify receipt and interpretation

Run on 12 October 2026 against Multica `b115ec80f1748d8d2766481706b0ecb830389286` using Graphify `0.9.86` at `a5ba60ddf6293d9f380321965ecf1084391dcc2b`.

The tool was already installed in an isolated `/tmp` environment. Its [pinned README](https://github.com/Graphify-Labs/graphify/blob/a5ba60ddf6293d9f380321965ecf1084391dcc2b/README.md) and CLI implementation were inspected before invoking it. This uses its deterministic local AST pass, not its semantic LLM pass, cloud product or assistant skill installation. The tool repository declares Apache-2.0 and also ships a LICENSE-MIT and NOTICE; no tool code is redistributed here.

## Actual invocation

```sh
/tmp/lykeion-graphify-env/bin/graphify extract /tmp/multica-reference-source \
  --code-only --no-cluster --no-dedup --max-workers 2 \
  --exclude apps/mobile --exclude apps/docs \
  --out /tmp/lykeion-multica-graph-20261012 \
  > /tmp/lykeion-multica-graph-20261012.log 2>&1
```

Successful output:

- 5,668 uncached selected code files processed.
- 47,292 nodes; 269,339 edges.
- 249,349 edges tagged EXTRACTED; 19,990 tagged INFERRED.
- Zero source nodes under `apps/mobile/` or `apps/docs/`, checked after extraction.
- Full raw output: `/tmp/lykeion-multica-graph-20261012/graphify-out/graph.json` (macOS resolves `/tmp` to `/private/tmp`). The output directory is approximately 196 MB, including caches/artifacts.
- Exact hash and relation counts: [graphify-receipt.json](graphify-receipt.json).
- LLM usage fields in the raw graph are recorded in that receipt; the invocation is code-only and no LLM extraction was requested.

## Queries actually run

```sh
/tmp/lykeion-graphify-env/bin/graphify explain TaskService \
  --graph /tmp/lykeion-multica-graph-20261012/graphify-out/graph.json
```

This name was ambiguous across seven nodes. The tool suggested disambiguating by source path; this was done:

```sh
/tmp/lykeion-graphify-env/bin/graphify explain \
  server/internal/service/task.go::TaskService \
  --graph /tmp/lykeion-multica-graph-20261012/graphify-out/graph.json
```

It resolved `TaskService` at `server/internal/service/task.go:L39`, degree 186, with direct references to persistence queries, event bus, realtime hub, metrics and task lifecycle methods. A metadata-only export of those 186 edges and 187 nodes is saved as [graphify-task-service-neighborhood.json](graphify-task-service-neighborhood.json). No implementation bodies are included.

A shortest-path query between `TaskService` and scheduler `Manager` also ran:

```sh
/tmp/lykeion-graphify-env/bin/graphify path \
  server_internal_service_task_go_service_taskservice \
  server_internal_scheduler_manager_go_scheduler_manager \
  --graph /tmp/lykeion-multica-graph-20261012/graphify-out/graph.json
```

It returned a 23-hop path mixed with test files, common symbols and inferred call edges. This is **not useful evidence of an architectural or runtime dependency** and was rejected as such. Direct source inspection—not this path—supports the inventory's statements about scheduling and task semantics.

## Limits that affect interpretation

- One file, `packages/views/runtimes/components/runtime-detail-rail.test.tsx`, produced a syntax error at line 26 and yielded no symbols.
- 785 code files yielded no symbols. Some are data/configuration files or source forms unsupported by the extractor; absence of a symbol cannot establish absence of a capability.
- Tests and generated code were not globally excluded. Their nodes/edges contribute to totals, so degree is not a clean measure of production responsibility.
- No semantic pass over documentation, PDFs or prose; these were read manually for the questions in the inventory. No clustering/community analysis and no entity deduplication were requested.
- EXTRACTED means a source relation was parsed, not that the whole application was exercised. INFERRED relationships need source verification. Name collisions and mixed test/runtime paths make uncritical graph traversal unsafe.
- This version's raw extraction JSON has `nodes`, `edges`, `hyperedges`, `input_tokens` and `output_tokens`, without the graph-level version metadata described in the current README. Version/commit provenance is recorded in our separate receipt instead.
- No full graph HTML was generated. The authored capability map below is a reading aid, not Graphify output.

## Authored capability map

[capability-map.mmd](capability-map.mmd) is an original, coarse interpretation of source responsibilities. Its arrows describe intended information/control boundaries; they do not claim to be an automatically recovered runtime trace or a required Lykeion architecture.
