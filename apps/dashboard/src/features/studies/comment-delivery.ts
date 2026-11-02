import type { CommentDelivery } from '../../api/types';

// Where a comment went, as the study page says it under the comment. `who`
// names the agent that took it; a study that is no longer working has no
// step left to queue a comment for, so an undelivered comment says nothing.
export function deliveryLine(
  d: CommentDelivery | null | undefined,
  { working, who }: { working: boolean; who?: string },
): string {
  if (!d) return working ? 'Queued for the next step' : '';
  const name = who || (d.role === 'leader' ? 'the leader' : 'a Team member');
  switch (d.how) {
    case 'running_turn':
      return `Delivered to ${name}'s running turn`;
    case 'member_result':
      return 'Given to the leader while it waited on a member';
    case 'next_run':
      return `Included in ${name}'s next step`;
  }
}
