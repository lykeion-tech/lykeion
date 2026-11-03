# Task 3 report

Implemented local TEI embedding adapter, deterministic fake, Redis Search index, and optional server initialization.

- Pinned `Snowflake/snowflake-arctic-embed-s`, 384 dimensions, exact query prefix `Represent this sentence for searching relevant passages: `. Deployment plan retains `ghcr.io/huggingface/text-embeddings-inference:cpu-1.9`; no deployment changes in this task.
- TEI batches 32, normalizes/truncates, validates vector count/dimensions/finite values, uses bounded HTTP timeouts, and suppresses upstream response/error bodies that could contain text or endpoint credentials.
- Index stores workspace, kind, model, ref, **version**, embedding only; no text. Both Redis RESP2 and default RESP3 response formats are supported. Every candidate is workspace-filtered and checked again on decode; callers must re-read live under the asker.
- Required API adjustment: `Put(ctx, ws, ref, version, text)` (five arguments), and `PutVersion` with the same arguments. Empty/whitespace versions are rejected. Task 4 must provide the live record's version.
- Searches also filter the pinned model. Model changes immediately invalidate old entries as candidates. Task 4 must trigger a full source backfill on model change to restore coverage; no mixed-model search is permitted.
- Server sets index nil when URL is absent or Ensure fails, warns once without endpoint/keys on startup failure. A temporary TEI outage warns but retains the index so later requests/indexing can recover. Later wiring tasks should attach `labIndex` to their consumer/tool services (currently explicit placeholder).

Verification: `LYK_INDEX_INTEGRATION=1 go test ./internal/embed/ ./internal/labindex/ ./cmd/server/` PASS. Tests exercised 70-input batching/query prefix/no caller mutation; malformed responses; required version; float32 little-endian format; actual Redis 8 workspace separation, nearest-first ordering, deletion, stored version/no text, model invalidation, repeated Ensure; Redis 7 ErrUnavailable. Initial RESP3 test failed and was fixed, then full focused run passed. Initial sandbox HTTP bind denial required escalated focused test execution.

Containers were uniquely named `lyk-ari-task3-<uuid>` with ephemeral loopback ports, removed by test cleanup. No preview containers/data were used. Integration container tests are opt-in via `LYK_INDEX_INTEGRATION=1` so ordinary unit runs never create containers implicitly.

Concern: no real TEI model container was downloaded/run in this bounded task; HTTP contract is tested using httptest. Full model-change backfill belongs to Task 4 consumer orchestration.

## Review follow-up: existing schema readiness

`Ensure` now inspects `FT.INFO` when `idx:lab` exists. It requires HASH keys with exactly the `lab:` prefix; indexed workspace/kind/model TAG attributes with matching identifiers; and indexed embedding VECTOR FLAT FLOAT32 with 384 dimensions and COSINE distance. Incompatible schemas return an error so startup falls back; existing indexes are never deleted or rebuilt by readiness.

Added isolated Redis 8 integration cases for missing model TAG, wrong vector dimensions, and wrong key prefix; each asserts rejection and preservation of the existing index. The existing repeated-Ensure case verifies compatible acceptance. `LYK_INDEX_INTEGRATION=1 go test ./internal/labindex/` PASS. Only uniquely named owned test containers were created and cleaned up.
