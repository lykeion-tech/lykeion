export function buildDesktopUpdater(root: string, output: string, sourceOverride?: string, beforePublish?: () => Promise<void>): Promise<void>;
export function auditBundleSource(source: string): void;
export function readPackageFile(directory: string, name: string): Promise<string>;
