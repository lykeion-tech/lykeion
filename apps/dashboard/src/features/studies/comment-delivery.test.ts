import { expect, it } from 'vitest';
import { deliveryLine } from './comment-delivery';

it('says where each comment went', () => {
  expect(deliveryLine({ how: 'running_turn', role: 'leader' })).toBe(
    "Delivered to the leader's running turn",
  );
  expect(deliveryLine({ how: 'member_result', role: 'leader' })).toBe(
    'Given to the leader while it waited on a member',
  );
  expect(deliveryLine({ how: 'next_run', role: 'member-1' })).toBe(
    "Included in the member-1's next step",
  );
  expect(deliveryLine(null)).toBe('Queued for the next step');
});
