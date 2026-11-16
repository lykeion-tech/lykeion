export type DownloadPlatform = 'mac-arm64' | 'mac-x64' | 'linux-arm64' | 'linux-x64';

export function downloadPlatform(platform: string, architecture: string): DownloadPlatform | null {
  const os = platform.toLowerCase();
  const cpu = architecture.toLowerCase();
  const arch = /^(arm|arm64|aarch64)$/.test(cpu) ? 'arm64' : /^(x86|x64|x86_64|amd64)$/.test(cpu) ? 'x64' : null;
  if (!arch) return null;
  if (os === 'macos' || os === 'mac') return `mac-${arch}`;
  if (os === 'linux') return `linux-${arch}`;
  return null;
}
