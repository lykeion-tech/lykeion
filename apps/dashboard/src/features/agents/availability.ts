import type { AgentDefinition, Runtime } from '../../api/types';

export function agentAvailable(agent: AgentDefinition, runtimes: Runtime[], userId?: string) {
  return !!userId && !agent.archived_at && runtimes.some((runtime) =>
    runtime.owner_id === userId && runtime.harness === agent.harness &&
    runtime.status === 'online' && !runtime.unhealthy_reason,
  );
}
