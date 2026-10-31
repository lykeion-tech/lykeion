export type AgentConnector = { name: string; url: string };

// Enable only when agent reads and writes provide the checked server list.
export const agentConnectorIntegrationAvailable = false;

function urlProblem(raw: string): string | undefined {
  if (new TextEncoder().encode(raw).length > 2048) return 'URL longer than 2048 bytes.';
  if (/[^\x21-\x7e]/.test(raw)) return 'URL must be printable ASCII with no spaces.';
  if (/[?#]/.test(raw)) return 'URL may not have a query or fragment.';
  const match = /^https?:\/\/([^/]*)(.*)$/i.exec(raw);
  if (!match) return 'Use an http or https URL with a host.';
  const host = match[1]!;
  if (!host) return 'URL needs a host.';
  if (host.includes('@')) return 'URL carries credentials; agent servers take none.';
  if (/%(?![\da-f]{2})/i.test(raw)) return 'URL contains an invalid percent escape.';

  // Validate authority separately from WHATWG URL: Go accepts numeric ports
  // outside the browser range, percent-encoded host bytes and scoped IPv6.
  const hostByte = /^[a-z\d!$&'()*+,;=:[\]<>"_.~-]$/i;
  let zone = false;
  for (let i = 0; i < host.length; i++) {
    const char = host[i]!;
    if (char === '%') {
      const byte = parseInt(host.slice(i + 1, i + 3), 16);
      if (host.startsWith('[') && host.slice(i, i + 3) === '%25') zone = true;
      if (
        zone
          ? byte !== 37 && byte !== 32 && (byte > 127 || !hostByte.test(String.fromCharCode(byte)))
          : byte < 128 && byte !== 37
      )
        return 'URL contains an invalid host escape.';
      i += 2;
    } else if (!hostByte.test(char)) return 'URL contains an invalid host character.';
  }
  if (host.startsWith('[')) {
    const end = host.lastIndexOf(']');
    if (end < 0 || host.lastIndexOf('[') !== 0 || !/^(?::\d*)?$/.test(host.slice(end + 1)))
      return 'URL contains an invalid host or port.';
    const address = host.slice(1, end),
      zoneAt = address.indexOf('%25');
    if (zoneAt >= 0 && zoneAt + 3 === address.length) return 'URL contains an invalid IPv6 zone.';
    try {
      new URL(`http://[${zoneAt < 0 ? address : address.slice(0, zoneAt)}]/`);
    } catch {
      return 'URL contains an invalid IPv6 host.';
    }
  } else {
    if (host.includes('[')) return 'URL contains an invalid IP literal.';
    const colon = host.indexOf(':');
    if (colon >= 0 && !/^:\d*$/.test(host.slice(colon))) return 'URL contains an invalid port.';
  }
  return undefined;
}

export function validateAgentConnectors(servers: AgentConnector[]): string | undefined {
  if (servers.length > 8) return 'At most 8 MCP servers are allowed.';
  const names = new Set<string>();
  for (const server of servers) {
    if (Object.keys(server).some((key) => key !== 'name' && key !== 'url'))
      return 'Agent servers have only a name and a URL.';
    if (typeof server.name !== 'string' || typeof server.url !== 'string')
      return 'Agent server name and URL must be strings.';
    if (server.name === 'sources' || server.name.startsWith('lyk'))
      return 'This name is reserved: sources and names starting with lyk belong to Lykeion.';
    if (!/^[a-z][a-z0-9_-]{0,27}$/.test(server.name))
      return 'Use 1 to 28 lowercase letters, digits, _ or -, starting with a letter.';
    if (names.has(server.name)) return `MCP server name "${server.name}" appears twice.`;
    names.add(server.name);
    const problem = urlProblem(server.url);
    if (problem) return `${server.name}: ${problem}`;
  }
  return undefined;
}

export function removeAgentConnector(servers: AgentConnector[], name: string): AgentConnector[] {
  return servers.filter((server) => server.name !== name).map(({ name, url }) => ({ name, url }));
}
