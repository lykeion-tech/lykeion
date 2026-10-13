// Every cell is quoted. A cell a spreadsheet would read as a formula (=, +, - or @, also after
// leading whitespace) or that starts with a tab or line break gets a leading apostrophe.
export function csvCell(value: string) {
  return (
    '"' + (/^(?:\s*[=+@-]|[\t\r\n])/.test(value) ? "'" + value : value).replaceAll('"', '""') + '"'
  );
}

export function toCsv(rows: string[][]) {
  return rows.map((row) => row.map(csvCell).join(',')).join('\r\n');
}
