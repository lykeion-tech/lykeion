export interface UpdateBuildOptions {
  anchors: string;
  version: string;
  keyIDs: string[];
}
export function desktopGoEnvironment(input: NodeJS.ProcessEnv, trustedRelease?: boolean): NodeJS.ProcessEnv;
export function updateBuildOptions(
  anchors: string | undefined,
  version: string,
  platform: string,
  arch: string,
  hostPlatform: string,
  hostArch: string,
): UpdateBuildOptions | undefined;
export function updateBuildFlags(options: UpdateBuildOptions | undefined): string[];
export function verifyUpdateBuildInfo(raw: string, options: UpdateBuildOptions): void;
