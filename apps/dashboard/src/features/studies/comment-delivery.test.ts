import { expect, it } from 'vitest';
import { deliveryLine } from './comment-delivery';

const working = { working: true };

it('says where each comment went, by the agent the member is', () => {
  expect(deliveryLine({ how: 'running_turn', role: 'leader' }, working)).toBe(
    "Delivered to the leader's running turn",
  );
  expect(deliveryLine({ how: 'member_result', role: 'leader' }, working)).toBe(
    'Given to the leader while it waited on a member',
  );
  expect(
    deliveryLine({ how: 'next_run', role: 'member-1' }, { working: false, who: 'Searcher' }),
  ).toBe("Included in Searcher's next step");
  expect(deliveryLine({ how: 'next_run', role: 'member-1' }, working)).toBe(
    "Included in a Team member's next step",
  );
});

it('queues an undelivered comment only while the study works', () => {
  expect(deliveryLine(null, working)).toBe('Queued for the next step');
  expect(deliveryLine(null, { working: false })).toBe('');
});
