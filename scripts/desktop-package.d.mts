export function desktopTarget(
  platform: string,
  arch: string,
): {
  goos: string;
  goarch: string;
  builder: string[];
};
export function desktopBuilderArgs(
  target: { builder: string[] },
  directory: boolean,
  signing: { identity: string; keychainProfile?: string; keychain?: string } | undefined,
  signingIntermediate?: boolean,
  maintainer?: string,
): string[];
export function releaseOrigin(value?: string): string;
export function debianMaintainer(
  platform: string,
  args: string[],
  value?: string,
): string | undefined;
export function verifyGoBuild(info: string, commit: string, goos: string, goarch: string): void;
export function verifyBundledBinary(built: string, bundled: string): void;
export function safeBuildEnvironment(input: NodeJS.ProcessEnv): NodeJS.ProcessEnv;
export function macSigningOptions(
  platform: string,
  input: NodeJS.ProcessEnv,
): { identity: string; keychainProfile?: string; keychain?: string } | undefined;
export function macBuilderSigningArgs(
  signing: { identity: string; keychainProfile?: string; keychain?: string } | undefined,
  intermediate: boolean,
): string[];
export function extractTrackedSnapshot(repo: string, destination: string): void;
export function replaceOutput(source: string, output: string): void;
export function verifyArchive(
  archive: string,
  expected: { version: string; commit: string; platform: string; arch: string; updaterSHA256: string },
  hostedOrigin: string,
  asar: typeof import('@electron/asar'),
): void;
export function macFeedRename(platform: string, arch: string): [string, string] | null;
