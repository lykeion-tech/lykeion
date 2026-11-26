import { expect, it } from 'vitest';
import { deliveryLine, composerAvailability } from './comment-delivery';

it('explains when saved file messages still need an eligible run', () => {
  expect(deliveryLine(null, { working: true, files: true, reply: true, who: 'Ada' })).toBe(
    'Saved · files will reach Ada only if Ada has a later eligible run; this reply does not start one',
  );
  expect(deliveryLine(null, { working: true, files: true })).toBe(
    'Saved · files are pending delivery in an eligible run',
  );
  expect(deliveryLine({ how: 'next_run', role: 'leader' }, { working: true, files: true })).toBe(
    'Delivered to the leader',
  );
});

it('waits for acknowledged input rather than treating queued work as delivered', () => {
  expect(deliveryLine(null, { working: true })).toBe('');
  expect(deliveryLine(null, { working: true, unavailable: 'Agent runtime is offline' })).toBe(
    'Pending · Agent runtime is offline',
  );
  expect(deliveryLine({ how: 'next_run', role: 'leader' }, { working: true, who: 'Codex' })).toBe(
    'Delivered to Codex',
  );
});
it('makes closed, cancelled, and run-reply behavior explicit', () => {
  expect(deliveryLine(null, { working: false })).toBe(
    'Comment saved · this study cannot start agent work',
  );
  expect(deliveryLine(null, { working: true, cancelled: true })).toBe(
    'Cancelled · will not be delivered',
  );
  expect(deliveryLine(null, { working: true, reply: true })).toBe(
    'Comment saved · replies do not start agent work',
  );
  expect(deliveryLine({ how: 'running_turn', role: 'leader' }, { working: true })).toBe(
    "Delivered to the leader's running turn",
  );
  expect(deliveryLine({ how: 'member_result', role: 'leader' }, { working: true })).toBe(
    'Given to the leader while it waited on a member',
  );
});
it('reports known queued-runtime and start failures without guessing availability', () => {
  expect(
    composerAvailability(
      'working',
      [{ status: 'queued', runtime_id: 'r' }],
      [{ id: 'r', status: 'offline' }],
    ),
  ).toBe('Agent runtime is offline');
  expect(composerAvailability('working', [{ status: 'failed', runtime_id: null }], [])).toBe(
    'The last agent run failed; waiting for recovery',
  );
  expect(composerAvailability('blocked', [], [])).toBe('');
  expect(composerAvailability('working', [{ status: 'queued', runtime_id: null }], [])).toBe('');
});

it('reports the server offline reason before a queued run is bound to a runtime', () => {
  expect(
    composerAvailability(
      'working',
      [{ status: 'queued', runtime_id: null, queue_reason: 'runtime_offline' }],
      [],
    ),
  ).toBe('Agent runtime is offline');
});

it('prioritizes an active lead over a newer member failure without claiming delivery', () => {
  const unavailable = composerAvailability(
    'working',
    [
      { status: 'running', runtime_id: 'leader-runtime' },
      { status: 'failed', runtime_id: 'member-runtime' },
    ],
    [],
  );
  expect(unavailable).toBe('');
  expect(deliveryLine(null, { working: true, unavailable })).toBe('');
});

it('shows the server upgrade message for a queued frozen comparison', () => {
  const waiting =
    'Waiting for a daemon with output attestation. Upgrade the daemon for this frozen comparison.';
  const run = { status: 'queued', runtime_id: null, requires_output_attestation: true, waiting };
  expect(composerAvailability('working', [run], [])).toBe(waiting);
  expect(composerAvailability('working', [{ ...run, requires_output_attestation: false }], [])).toBe('');
});
