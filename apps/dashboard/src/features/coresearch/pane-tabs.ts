// Ported from Lykeion-L2 RightPaneTabs.tsx.
export interface RightPaneTab {
  id: string;
  label: string;
  title?: string;
  closable?: boolean;
  closeLabel?: string;
}
export const PANE_TABS: RightPaneTab[] = [
  { id: 'notebook', label: 'Notebook' },
  { id: 'files', label: 'Files' },
  { id: 'compute', label: 'Compute' },
];
export function tabAfterClose(tabs: readonly RightPaneTab[], closedId: string): string | null {
  const at = tabs.findIndex((t) => t.id === closedId);
  if (at < 0) return null;
  return (tabs[at + 1] ?? tabs[at - 1])?.id ?? null;
}
