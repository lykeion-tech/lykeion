# S02 artifacts and independent authority — started

The first implemented increment protects and exposes the exact sealed configuration bytes introduced as S01 prerequisites. Migration 008 prevents in-place edits/deletion of the logical manifest. The new author-scoped content read validates both manifest and blob integrity, returns the pinned reference, and is available through CLI GET and the allowlisted native bridge. No submitted configuration is automatically a scientifically accepted artifact.

Tests use an isolated PostgreSQL schema and temporary CAS: authorized content round-trip, denial to another workspace member, attempted database rewrite/delete, and tampered blob rejection on both metadata and content routes. Contracts generate TypeScript and Python models for the same shape.

The running installed desktop Agents list and agent Overview were inspected read-only. Agents list evidence is captured in `research/visual-baseline/s02`. Further reference capture stopped when Computer Use reported that the Mac was locked. No S02 UI surface is claimed implemented or visually accepted by this backend increment.

Next increments follow the existing plan: general authored manifests and produced capture integration; role/configuration/roster authority; independent reviewer selection and sealed verdicts; structured scientific decisions and versioned jev policy/classification artifacts. Exit checks remain immutability, no self-review or reviewer shopping, stable replay, stale/comment denial and fail-closed classifier outage. These are not all complete.

Rollback: this additive migration contains no data rewrite. Stop writers and restore a verified database/CAS backup together for a full rollback; do not drop the immutability guard to patch a submitted artifact. Downgrading the application does not remove the guard.
