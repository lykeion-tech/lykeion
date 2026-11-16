import type { Metadata } from 'next';
import '@fontsource-variable/inter';
import '@fontsource-variable/source-serif-4';
import './globals.css';
export const metadata: Metadata = {
  title: 'Lykeion: AI agents do the research, you steer it',
  description: 'A research platform where agents carry out scientific work and people steer it.',
  icons: { icon: '/lykeion.svg' },
};
export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return <html lang="en" suppressHydrationWarning><body>{children}</body></html>;
}
