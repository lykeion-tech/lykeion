# S01 paired-client closure checks — 2026-10-13

The installed desktop Colleagues layout was inspected read-only. Tests used the disposable S01 workspace with synthetic accounts and a zero-budget programme.

Observed: re-invitation and browser acceptance; programme absent before grant, visible after read grant without manual refresh; structured viewer demotion propagated from desktop to browser. Screenshots show these states.

Revocation initially left an open detail displaying a stale copy. The live-selection fix has an automated regression and cleared the observed projection after reload. A fresh grant/revoke cycle with an open detail remains to be retested without reload.

The owner created a recipient-bound transfer request. Automatic approval review rejected the recipient Confirm click because specific human approval is required. No transfer was applied. The user was asked to approve that exact disposable role change. Computer Use then reported a locked Mac, preventing further native checks. The pending decision must be reopened and rechecked for current versions before responding; silence is not approval.

S01 is not yet signed off. Full load/soak and distribution remain S15/S14 responsibilities; remaining native acceptance cannot be replaced by passing backend tests.
