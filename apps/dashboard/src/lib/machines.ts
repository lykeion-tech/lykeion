import type { Runtime } from '../api/types';

export type Daemon = {
  id: string;
  name: string;
  ownerId: string;
  online: boolean;
  runtimes: Runtime[];
  lastSeen: string;
  activeRuns: number;
};

// A machine is the runtimes one daemon registered, one per agent CLI.
export function groupDaemons(runtimes: Runtime[]): Daemon[] {
  const byId = new Map<string, Runtime[]>();
  for (const r of runtimes) byId.set(r.daemon_id, [...(byId.get(r.daemon_id) ?? []), r]);
  return [...byId.entries()]
    .map(([id, list]) => {
      const sorted = [...list].sort((a, b) => a.harness.localeCompare(b.harness));
      return {
        id,
        name: list.find((r) => r.custom_name)?.custom_name ?? list[0]!.device_name,
        ownerId: list[0]!.owner_id,
        online: list.some((r) => r.status === 'online'),
        runtimes: sorted,
        lastSeen: list
          .map((r) => r.last_seen_at)
          .sort()
          .at(-1)!,
        activeRuns: list.reduce((sum, r) => sum + r.active_runs, 0),
      };
    })
    .sort((a, b) => Number(b.online) - Number(a.online) || a.name.localeCompare(b.name));
}

export function computerStartCommands(server: string, platform: 'unix' | 'windows') {
  const executable = platform === 'windows' ? '.\\lyk.exe' : './lyk';
  const origin = new URL(server).origin;
  const quoted = "'" + origin.replaceAll("'", platform === 'windows' ? "''" : "'\"'\"'") + "'";
  return `${executable} login --server ${quoted} --email you@lab.org\n${executable} daemon ${platform === 'windows' ? 'run' : 'start'}`;
}

// Owner machines remain usable on every platform, including while offline.
export function studyMachines(runtimes: Runtime[], userId: string): Daemon[] {
  if (!userId) return [];
  return groupDaemons(
    runtimes.filter(
      (r) =>
        r.owner_id === userId ||
        (r.os === 'linux' && (r.visibility === 'workspace' || r.shared_with?.includes(userId))),
    ),
  );
}
