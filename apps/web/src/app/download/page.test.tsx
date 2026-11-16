import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it } from 'vitest';
import DownloadPage from './page';

it('shows SSH-safe remote machine installation and connection commands', () => {
  const html = renderToStaticMarkup(<DownloadPage />);
  expect(html).toContain('https://raw.githubusercontent.com/lykeion-tech/lykeion/main/scripts/install.sh');
  expect(html).toContain('lyk login --email you@lab.org --server https://app.lykeion.tech');
  expect(html).toContain('lyk workspace switch');
  expect(html).toContain('lyk daemon start');
  expect(html).toContain('Copy install command');
  expect(html).toContain('bubblewrap');
  expect(html).toContain('macOS / Linux');
});
