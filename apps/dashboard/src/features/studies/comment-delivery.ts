import type { CommentDelivery } from '../../api/types';

// Where a comment went, as the study page says it under the comment.
export function deliveryLine(d: CommentDelivery | null | undefined): string {
  if (!d) return 'Queued for the next step';
  switch (d.how) {
    case 'running_turn':
      return `Delivered to the ${d.role}'s running turn`;
    case 'member_result':
      return 'Given to the leader while it waited on a member';
    case 'next_run':
      return `Included in the ${d.role}'s next step`;
  }
}
