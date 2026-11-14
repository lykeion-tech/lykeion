import path from 'node:path';

export function desktopUserData(appData: string, packaged: boolean): string {
  return path.join(appData, packaged ? 'Lykeion' : 'Lykeion Preview');
}
