import { expect, it } from 'vitest';
import { chemicalFrame } from './chemical-frame';
it('isolates the viewer and encodes file content as inert data', () => {
  const html = chemicalFrame(
    'export const createViewer=()=>{};',
    '</script><script>alert(1)</script>',
    'pdb',
    1,
  );
  expect(html).toContain("default-src 'none'");
  expect(html).toContain("connect-src 'none'");
  expect(html).not.toContain('</script><script>alert');
  expect(html).toContain('\\u003c/script>');
});
