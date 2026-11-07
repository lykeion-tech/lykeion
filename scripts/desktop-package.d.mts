export function desktopTarget(
  platform: string,
  arch: string,
): {
  goos: string;
  goarch: string;
  builder: string[];
};
export function releaseOrigin(value?: string): string;
