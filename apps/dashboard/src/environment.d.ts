declare const __LYK_PUBLIC_API_ORIGIN__: string;

interface Window {
  lykeionDesktop?: {
    platform: string;
    openDocumentation(): Promise<void>;
    cancelRequest?(id: string): void;
    requestDocument?: string;
    reserveAuth?(path: string): string | null;
    releaseAuth?(token: string): void;
    daemon?: import('../../desktop/src/daemon-types.js').DesktopDaemonBridge;
  };
}
