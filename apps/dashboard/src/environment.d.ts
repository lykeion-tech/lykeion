declare const __LYK_PUBLIC_API_ORIGIN__: string;

interface Window {
  lykeionDesktop?: {
    platform: string;
    openDocumentation(): Promise<void>;
    openGoogleSignIn(): Promise<void>;
    daemon?: import('../../desktop/src/daemon-types.js').DesktopDaemonBridge;
  };
}
