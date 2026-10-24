const text = (value: unknown): string => (typeof value === 'string' ? value : '');
const record = (value: unknown): Record<string, unknown> => {
  if (typeof value === 'string') {
    try {
      return record(JSON.parse(value));
    } catch {
      return {};
    }
  }
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
};

// Parse only enough shell structure to classify an action. No token is displayed.
function shellTitle(command: string): string {
  const tokens: string[] = command.match(/"(?:\\.|[^"\\])*"|'[^']*'|&&|[^\s]+/g) ?? [];
  const unquote = (s: string) => (/^(['"]).*\1$/s.test(s) ? s.slice(1, -1) : s);
  while (tokens.length) {
    let executable = unquote(tokens.shift()!).split('/').pop()!;
    if (executable === 'env' || /^[A-Za-z_][A-Za-z0-9_]*=/.test(executable)) continue;
    if (executable === 'cd') {
      const next = tokens.indexOf('&&');
      if (next < 0) return 'Change working directory';
      tokens.splice(0, next + 1);
      continue;
    }
    if (['bash', 'sh', 'zsh', 'fish'].includes(executable)) {
      const flag = tokens.findIndex((t) => /^-[a-z]*c[a-z]*$/.test(t));
      return flag >= 0 && tokens[flag + 1]
        ? shellTitle(unquote(tokens[flag + 1]!))
        : 'Run a shell command';
    }
    executable = executable.replace(/\d+(?:\.\d+)*$/, '');
    if (executable === 'lyk') {
      const subject = tokens[0];
      const verb = tokens[1];
      const object =
        subject === 'record'
          ? 'research record'
          : subject === 'output'
            ? 'run output'
            : subject === 'run'
              ? 'research run'
              : '';
      if (object) {
        if (verb === 'put') return `Save a ${object}`;
        if (verb === 'get') return `Read a ${object}`;
        if (verb === 'list') return `List ${object}s`;
        return subject === 'run' ? 'Manage research runs' : `Manage ${object}s`;
      }
      return 'Use the Lykeion CLI';
    }
    const names: Record<string, string> = {
      rg: 'Search files',
      grep: 'Search files',
      find: 'Find files',
      ls: 'List files',
      cat: 'Read a file',
      head: 'Read file contents',
      tail: 'Read file contents',
      python: 'Run a Python script',
      Rscript: 'Run an R script',
      node: 'Run a JavaScript script',
      pytest: 'Run tests',
      go: 'Run a Go command',
      git: 'Inspect or update repository',
      curl: 'Request remote data',
      wget: 'Download remote data',
      pwd: 'Show working directory',
      mkdir: 'Create a directory',
      cp: 'Copy files',
      mv: 'Move files',
      rm: 'Remove files',
    };
    return names[executable] ?? 'Run a shell command';
  }
  return 'Run a shell command';
}

export function toolStepTitle(tool: string, input: Record<string, unknown>): string {
  let name = tool.replace(/ToolCall$/, '').replace(/^mcp(?:__|\.)[^.]+?(?:__|\.)/, '');
  if (name === 'call_tool') {
    name = text(input.tool).replace(/^mcp(?:__|\.)[^.]+?(?:__|\.)/, '');
    input = record(input.args ?? input.arguments);
  }
  const notebook = /^execute_([a-z][a-z0-9+_-]*)_cell$/i.exec(name);
  if (notebook || name === 'run_code') {
    const lang = (notebook?.[1] ?? text(input.language)).toLowerCase();
    const languages: Record<string, string> = {
      python: 'a Python',
      r: 'an R',
      shell: 'a shell',
      sql: 'an SQL',
      julia: 'a Julia',
      javascript: 'a JavaScript',
    };
    return languages[lang] ? `Run ${languages[lang]} notebook cell` : 'Run a notebook cell';
  }
  const command = text(input.command) || text(input.CommandLine) || text(input.cmd);
  if (command || /^(bash|shell|run_command|exec_command|command_execution)$/i.test(name))
    return shellTitle(command);
  if (/^(read|read_file|view_file|read_file_tool)$/i.test(name)) return 'Read a file';
  if (/^(edit|edit_file|multiedit|notebookedit|replace_file_content|apply_patch)$/i.test(name))
    return 'Edit a file';
  if (/^(write|write_file|write_to_file)$/i.test(name)) return 'Write a file';
  if (/^(grep|glob|find|grep_search|find_by_name)$/i.test(name)) return 'Search files';
  if (/^(search|web_search|search_query)$/i.test(name)) return 'Search for information';
  if (/^(fetch|webfetch|open_url|browse_page)$/i.test(name)) return 'Read a web page';
  return 'Use research tool';
}
