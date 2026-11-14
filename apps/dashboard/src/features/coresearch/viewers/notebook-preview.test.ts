import { describe, expect, it } from 'vitest';
import { notebookPreview } from './notebook-preview';

describe('Jupyter notebook previews', () => {
  it('preserves cell order, text output, safe raster images, and errors', () => {
    const preview = notebookPreview(
      JSON.stringify({
        nbformat: 4,
        cells: [
          { cell_type: 'markdown', source: ['# Findings\n', 'Details'] },
          {
            cell_type: 'code',
            source: ['print(2)'],
            outputs: [
              { output_type: 'stream', text: ['2\n'] },
              {
                output_type: 'display_data',
                data: { 'image/png': 'aGVsbG8=', 'text/html': '<img src=x onerror=alert(1)>' },
              },
              {
                output_type: 'error',
                ename: 'ValueError',
                evalue: 'bad',
                traceback: ['bad stack'],
              },
            ],
          },
        ],
      }),
    );
    expect(preview?.cells[0]?.source).toContain('# Findings');
    expect(preview?.cells[1]?.outputs).toEqual([
      { kind: 'text', text: '2\n' },
      { kind: 'image', src: 'data:image/png;base64,aGVsbG8=' },
      { kind: 'text', text: 'bad stack' },
    ]);
    expect(JSON.stringify(preview)).not.toContain('onerror');
  });
  it('rejects malformed notebooks and executable image payloads', () => {
    expect(notebookPreview('{bad')).toBeUndefined();
    expect(notebookPreview('{"cells":42}')).toBeUndefined();
    const p = notebookPreview(
      JSON.stringify({
        cells: [
          {
            cell_type: 'code',
            outputs: [
              {
                data: {
                  'image/svg+xml': '<svg onload=alert(1)>',
                  'image/png': 'not!base64',
                  'text/plain': 'safe',
                },
              },
            ],
          },
        ],
      }),
    );
    expect(p?.cells[0]?.outputs).toEqual([{ kind: 'text', text: 'safe' }]);
  });
  it('extracts notebook HTML table cells as plain text', () => {
    const p = notebookPreview(
      JSON.stringify({
        cells: [
          {
            cell_type: 'code',
            outputs: [
              {
                data: {
                  'text/html':
                    '<table><tr><th>Name</th><th>Value</th></tr><tr><td>alpha &amp; beta</td><td><img src=x onerror=alert(1)>42</td></tr></table>',
                },
              },
            ],
          },
        ],
      }),
    );
    expect(p?.cells[0]?.outputs).toEqual([
      {
        kind: 'table',
        rows: [
          ['Name', 'Value'],
          ['alpha & beta', '42'],
        ],
      },
    ]);
  });
  it('uses notebook language metadata and defaults to plain text', () => {
    expect(
      notebookPreview(JSON.stringify({ metadata: { language_info: { name: 'julia' } }, cells: [] }))
        ?.language,
    ).toBe('julia');
    expect(notebookPreview(JSON.stringify({ cells: [] }))?.language).toBe('text');
  });
  it('handles a long malformed table without pathological parsing', () => {
    const input = JSON.stringify({
      cells: [
        {
          cell_type: 'code',
          outputs: [{ data: { 'text/html': '<table>' + '<tr '.repeat(40000) } }],
        },
      ],
    });
    const start = performance.now();
    expect(notebookPreview(input)).toBeDefined();
    expect(performance.now() - start).toBeLessThan(1000);
  }, 10000);
});
