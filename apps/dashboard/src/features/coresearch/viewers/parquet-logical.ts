import type { CompactValue } from './compact-thrift';
type Fields = Map<number, CompactValue>;
export interface LogicalColumn {
  label: string;
  text: boolean;
  format: (value: bigint) => string;
}
const identity = (value: bigint) => String(value);
const map = (value: CompactValue | undefined): Fields => {
  if (!(value instanceof Map)) throw Error();
  return value;
};
const integer = (value: CompactValue | undefined): number => {
  if (typeof value !== 'bigint' || value < 0n || value > 1000n) throw Error();
  return Number(value);
};
const empty = (v: CompactValue | undefined) => {
  if (map(v).size !== 0) throw Error();
};
export function logicalColumn(schema: Fields, type: number, physicalLabel: string): LogicalColumn {
  const converted = schema.has(6) ? integer(schema.get(6)) : undefined;
  let kind: 'string' | 'date' | 'decimal' | 'time' | 'timestamp' | undefined,
    unit: 'ms' | 'µs' | 'ns' | undefined,
    utc: boolean | undefined,
    scale: number | undefined,
    precision: number | undefined;
  if (schema.has(10)) {
    const logical = map(schema.get(10));
    if (logical.size !== 1) throw Error();
    if (logical.has(1)) {
      empty(logical.get(1));
      kind = 'string';
    } else if (logical.has(6)) {
      empty(logical.get(6));
      kind = 'date';
    } else if (logical.has(5)) {
      const d = map(logical.get(5));
      if (d.size !== 2) throw Error();
      kind = 'decimal';
      scale = integer(d.get(1));
      precision = integer(d.get(2));
    } else if (logical.has(7) || logical.has(8)) {
      kind = logical.has(7) ? 'time' : 'timestamp';
      const d = map(logical.get(kind === 'time' ? 7 : 8));
      if (d.size !== 2 || typeof d.get(1) !== 'boolean') throw Error();
      utc = d.get(1) as boolean;
      const u = map(d.get(2));
      if (u.size !== 1) throw Error();
      const [tag, v] = [...u.entries()][0]!;
      empty(v);
      unit = tag === 1 ? 'ms' : tag === 2 ? 'µs' : tag === 3 ? 'ns' : undefined;
      if (!unit) throw Error();
    } else throw Error();
  }
  if (converted !== undefined) {
    const legacy: Record<number, [typeof kind, typeof unit, boolean | undefined]> = {
      0: ['string', undefined, undefined],
      5: ['decimal', undefined, undefined],
      6: ['date', undefined, undefined],
      7: ['time', 'ms', true],
      8: ['time', 'µs', true],
      9: ['timestamp', 'ms', true],
      10: ['timestamp', 'µs', true],
    };
    const old = legacy[converted];
    if (!old) throw Error();
    if (kind && (kind !== old[0] || (old[1] && unit !== old[1]))) throw Error();
    if (!kind) {
      [kind, unit, utc] = old;
    }
  }
  if (kind === 'decimal') {
    const oldScale = schema.has(7) ? integer(schema.get(7)) : undefined,
      oldPrecision = schema.has(8) ? integer(schema.get(8)) : undefined;
    if (scale === undefined) scale = oldScale;
    else if (oldScale !== undefined && oldScale !== scale) throw Error();
    if (precision === undefined) precision = oldPrecision;
    else if (oldPrecision !== undefined && oldPrecision !== precision) throw Error();
    if (
      scale === undefined ||
      precision === undefined ||
      precision < 1 ||
      scale > precision ||
      ![1, 2].includes(type) ||
      precision > (type === 1 ? 9 : 18)
    )
      throw Error();
    const p = precision,
      s = scale,
      limit = 10n ** BigInt(p);
    return {
      label: `DECIMAL(${p},${s})`,
      text: false,
      format(value) {
        const negative = value < 0n,
          abs = negative ? -value : value;
        if (abs >= limit) throw Error();
        const digits = String(abs).padStart(s + 1, '0');
        return (negative ? '-' : '') + (s ? digits.slice(0, -s) + '.' + digits.slice(-s) : digits);
      },
    };
  }
  if (schema.has(7) || schema.has(8)) throw Error();
  if (!kind) return { label: physicalLabel, text: false, format: identity };
  if (kind === 'string') {
    if (type !== 6) throw Error();
    return { label: physicalLabel, text: true, format: identity };
  }
  if (kind === 'date') {
    if (type !== 1) throw Error();
    return { label: 'DATE · days since 1970-01-01', text: false, format: identity };
  }
  if (
    !unit ||
    utc === undefined ||
    (kind === 'timestamp' ? type !== 2 : type !== (unit === 'ms' ? 1 : 2))
  )
    throw Error();
  const u = unit,
    isTime = kind === 'time';
  const max = u === 'ms' ? 86400000n : u === 'µs' ? 86400000000n : 86400000000000n;
  return {
    label: `${kind.toUpperCase()} · ${u} since ${isTime ? 'midnight' : '1970-01-01'} · ${utc ? 'UTC-adjusted' : 'local'}`,
    text: false,
    format(value) {
      if (isTime && (value < 0n || value >= max)) throw Error();
      return String(value);
    },
  };
}
