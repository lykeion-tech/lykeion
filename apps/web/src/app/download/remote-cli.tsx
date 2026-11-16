'use client';

import { useState } from 'react';
import { Check, Copy } from 'lucide-react';

const commands = [
  { label: 'Install', command: 'curl -fsSL https://raw.githubusercontent.com/lykeion-tech/lykeion/main/scripts/install.sh | bash' },
  { label: 'Sign in with your email', command: 'lyk login --email you@lab.org --server https://app.lykeion.tech' },
  { label: 'Choose your workspace', command: 'lyk workspace switch <slug>' },
  { label: 'Start the daemon', command: 'lyk daemon start' },
];

export default function RemoteCLI() {
  const [error, setError] = useState('');
  const [copied, setCopied] = useState('');
  async function copy(label: string, command: string) {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(label);
      setError('');
    } catch {
      setCopied('');
      setError('Could not copy the command. Select the text to copy it.');
    }
  }
  return <section className="remote-cli" id="cli"><div className="download-wrap remote-cli-grid"><div className="remote-cli-intro"><span className="download-kicker">CLI</span><h2>On a server or a<br/>remote dev box?</h2><p>Install the CLI and connect the daemon to your lab. These commands work over SSH, too.</p><p>Install and sign in to Claude Code or Codex on the machine. Linux also needs bubblewrap; macOS uses its built-in sandbox.</p></div><div className="remote-cli-terminal"><div className="remote-cli-terminal-label">macOS / Linux</div>{commands.map(({ label, command }) => <div className="remote-cli-command" key={label}><span># {label}</span><div><code><span aria-hidden="true">$ </span>{command}</code><button type="button" aria-label={`Copy ${label.toLowerCase()} command`} onClick={() => void copy(label, command)}>{copied === label ? <Check size={15} aria-hidden="true"/> : <Copy size={15} aria-hidden="true"/>}</button></div></div>)}<p className="remote-cli-note">Replace the email and workspace slug with your own. If <code>lyk</code> is not on your PATH, follow the installer’s instructions.</p>{error && <span className="remote-cli-status" role="alert">{error}</span>}</div></div></section>;
}
