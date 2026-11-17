import type { DownloadPlatform } from './platform';

export const releasesUrl = 'https://github.com/lykeion-tech/lykeion/releases';
export const releaseApi = 'https://api.github.com/repos/lykeion-tech/lykeion/releases/latest';

type Release = { tag: string; date: string; files: Partial<Record<DownloadPlatform, Record<string, string>>> };

export function parseRelease(value: unknown): Release | null {
  if (!value || typeof value !== 'object') return null;
  const data = value as { tag_name?: unknown; published_at?: unknown; assets?: unknown };
  if (typeof data.tag_name !== 'string' || !/^v\d+\.\d+\.\d+$/.test(data.tag_name) ||
      typeof data.published_at !== 'string' || Number.isNaN(Date.parse(data.published_at)) || !Array.isArray(data.assets)) return null;
  const version = data.tag_name.slice(1);
  const names: Record<DownloadPlatform, Record<string, string>> = {
    'mac-arm64': { '.dmg': `Lykeion-${version}-arm64.dmg`, '.zip': `Lykeion-${version}-arm64-mac.zip` },
    'mac-x64': { '.dmg': `Lykeion-${version}.dmg`, '.zip': `Lykeion-${version}-mac.zip` },
    'linux-x64': { '.AppImage': `Lykeion-${version}.AppImage`, '.deb': `lykeion_${version}_amd64.deb` },
    'linux-arm64': { '.AppImage': `Lykeion-${version}-arm64.AppImage`, '.deb': `lykeion_${version}_arm64.deb` },
  };
  const uploaded = new Set(data.assets.filter(a => a && a.state === 'uploaded' && typeof a.name === 'string').map(a => a.name));
  const files: Release['files'] = {};
  for (const [platform, formats] of Object.entries(names)) {
    files[platform as DownloadPlatform] = Object.fromEntries(Object.entries(formats).filter(([, name]) => uploaded.has(name)).map(([format, name]) => [format, `${releasesUrl}/download/${data.tag_name}/${encodeURIComponent(name)}`]));
  }
  return { tag: data.tag_name, date: data.published_at, files };
}
