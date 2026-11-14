import { expect, it } from 'vitest';
import { parseMarkdownBlocks, renderMarkdown } from './markdown';

it('links plain DOI references without changing existing links or code', () => {
  const text =
    'DOI:10.3390/math5030039, doi:10.3758/s13423-015-0947-8 and 10.1002/sim.8086. [source](https://example.com) `10.1234/code`';
  for (const html of [
    renderMarkdown(text).html,
    renderMarkdown(text, true, true).html,
    parseMarkdownBlocks(text)
      .map((b) => b.html ?? '')
      .join(''),
  ]) {
    expect(html).toContain('href="https://doi.org/10.3390/math5030039"');
    expect(html).toContain('href="https://doi.org/10.3758/s13423-015-0947-8"');
    expect(html).toContain('href="https://doi.org/10.1002/sim.8086"');
    expect(html).toContain('href="https://example.com"');
    expect(html).toContain('<code>10.1234/code</code>');
  }
});
