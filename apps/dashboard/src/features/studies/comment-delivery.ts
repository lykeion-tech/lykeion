import type { CommentDelivery } from '../../api/types';

// Availability is advisory; only a persisted acknowledgement proves delivery.
export function composerAvailability(
  state: string,
  runs: readonly {
    status: string;
    runtime_id: string | null;
    cancel_reason?: string | null;
    queue_reason?: string | null;
  }[],
  runtimes: readonly { id: string; status: string }[],
): string {
  if (state !== 'working') return '';
  // A newer member failure does not mean the still-active lead needs recovery.
  if (runs.some((run) => run.status === 'running' || run.status === 'dispatched')) return '';
  const latest = [...runs].reverse().find((run) => run.status === 'queued') ?? runs.at(-1);
  if (!latest) return '';
  if (
    latest.status === 'queued' &&
    (latest.queue_reason === 'runtime_offline' ||
      runtimes.some((r) => r.id === latest.runtime_id && r.status === 'offline'))
  )
    return 'Agent runtime is offline';
  if (latest.status === 'failed') return 'The last agent run failed; waiting for recovery';
  if (latest.status === 'cancelled') return 'Agent work was interrupted; waiting for recovery';
  return '';
}

export function deliveryLine(
  d: CommentDelivery | null | undefined,
  {
    working,
    who,
    unavailable,
    cancelled,
    reply,
  }: {
    working: boolean;
    who?: string;
    unavailable?: string;
    cancelled?: boolean;
    reply?: boolean;
  },
): string {
  if (cancelled)
    return d
      ? 'Cancelled · already delivered input cannot be recalled'
      : 'Cancelled · will not be delivered';
  if (!d) {
    if (reply) return 'Comment saved · replies do not start agent work';
    if (unavailable) return `Pending · ${unavailable}`;
    return working ? '' : 'Comment saved · this study cannot start agent work';
  }
  const name = who || (d.role === 'leader' ? 'the leader' : 'a Team member');
  switch (d.how) {
    case 'running_turn':
      return `Delivered to ${name}'s running turn`;
    case 'member_result':
      return 'Given to the leader while it waited on a member';
    case 'next_run':
      return `Delivered to ${name}`;
  }
}
