// A connector's transport is edited either as fields or as the JSON object it is saved as.
// Keys the fields do not model are carried through untouched in `extra`.
export type ConnectorFields = {
  transport: string;
  command: string;
  url: string;
  args: string[];
  env: { key: string; value: string }[];
  headers: { key: string; value: string }[];
  extra: Record<string, unknown>;
};

const modelled = ['type', 'command', 'url', 'args', 'env', 'headers'];

export function connectorConfig(f: ConnectorFields) {
  return {
    ...f.extra,
    ...(f.transport === 'STDIO'
      ? {
          type: 'stdio',
          command: f.command,
          ...(f.args.length ? { args: f.args } : {}),
          ...(f.env.length ? { env: Object.fromEntries(f.env.map((x) => [x.key, x.value])) } : {}),
        }
      : {
          type: 'http',
          url: f.url,
          ...(f.headers.length
            ? { headers: Object.fromEntries(f.headers.map((x) => [x.key, x.value])) }
            : {}),
        }),
  };
}

// Throws SyntaxError for text that is not JSON, Error with a reason for a bad shape.
export function parseConnectorJson(text: string) {
  const value: unknown = JSON.parse(text);
  if (!value || typeof value !== 'object' || Array.isArray(value))
    throw new Error('Configuration must be a JSON object.');
  const config = value as Record<string, unknown>;
  if (!['stdio', 'http'].includes(String(config.type)))
    throw new Error('Transport type must be stdio or http.');
  if (config.type === 'stdio' && typeof config.command !== 'string')
    throw new Error('A STDIO configuration needs a command string.');
  if (config.type === 'http' && typeof config.url !== 'string')
    throw new Error('An HTTP configuration needs a URL string.');
  if (
    config.args !== undefined &&
    (!Array.isArray(config.args) || config.args.some((x) => typeof x !== 'string'))
  )
    throw new Error('Arguments must be an array of strings.');
  for (const key of ['env', 'headers']) {
    const entry = config[key];
    if (
      entry !== undefined &&
      (!entry ||
        typeof entry !== 'object' ||
        Array.isArray(entry) ||
        Object.values(entry).some((x) => typeof x !== 'string'))
    )
      throw new Error('Headers and environment values must be string maps.');
  }
  return config;
}

// Fields for a configuration that passed parseConnectorJson.
export function connectorFields(config: Record<string, unknown>): ConnectorFields {
  const pairs = (map: unknown) =>
    Object.entries((map as Record<string, string> | undefined) ?? {}).map(([key, value]) => ({
      key,
      value,
    }));
  return {
    transport: config.type === 'stdio' ? 'STDIO' : 'Streamable HTTP',
    command: typeof config.command === 'string' ? config.command : '',
    url: typeof config.url === 'string' ? config.url : '',
    args: (config.args as string[] | undefined) ?? [],
    env: pairs(config.env),
    headers: pairs(config.headers),
    extra: Object.fromEntries(Object.entries(config).filter(([key]) => !modelled.includes(key))),
  };
}
