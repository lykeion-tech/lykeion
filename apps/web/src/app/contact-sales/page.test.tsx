import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import ContactSales from './page';

describe('contact sales page', () => {
  it('presents an accessible contact form with separate consent choices', () => {
    const html = renderToStaticMarkup(<ContactSales />);
    expect(html).toContain('name="first_name"');
    expect(html).toContain('name="email"');
    expect(html).toContain('name="purpose"');
    expect(html).toContain('name="contact_consent"');
    expect(html).toContain('name="marketing_consent"');
    expect(html).toContain('name="processing_consent"');
    expect(html).toContain('href="/privacy/"');
    expect(html).toContain('type="submit"');
  });

  it('lists real regions once each', () => {
    const html = renderToStaticMarkup(<ContactSales />);
    const select = html.match(/<select name="country"[\s\S]*?<\/select>/)?.[0] ?? '';
    const values = Array.from(select.matchAll(/<option value="([^"]+)"/g), match => match[1]);
    expect(values.length).toBeGreaterThan(200);
    expect(new Set(values).size).toBe(values.length);
    expect(values).not.toContain('Unknown Region');
    expect(values).not.toContain('Pseudo-Accents');
  });
});
