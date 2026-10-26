export interface DaemonPreferences {
  autoStart: boolean;
  autoStop: boolean;
}
export interface DaemonSession {
  apiOrigin: string;
  userID: string;
  sessionToken: string;
}
export interface DaemonSnapshot {
  authenticatedUserID: string | null;
  status: 'running' | 'starting' | 'stopped' | 'unknown';
  availability: 'available' | 'unavailable';
  cliAvailable: boolean;
  error: string | null;
  pid: number | null;
  uptime: number | null;
  daemonID: string | null;
  profile: string | null;
  serverURL: string | null;
  deviceName: string | null;
  workspaceCount: number | null;
  os: string | null;
}
export interface DesktopDaemonBridge {
  getSnapshot(): Promise<DaemonSnapshot>;
  getPreferences(): Promise<DaemonPreferences>;
  setPreferences(preferences: DaemonPreferences): Promise<DaemonPreferences>;
  configureSession(session: DaemonSession): Promise<DaemonSnapshot>;
  clearSession(): Promise<DaemonSnapshot>;
  start(): Promise<DaemonSnapshot>;
  stop(): Promise<DaemonSnapshot>;
  restart(): Promise<DaemonSnapshot>;
  onStatusChange(listener: (snapshot: DaemonSnapshot) => void): () => void;
}
