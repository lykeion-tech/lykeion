const words = ['DISCOVERY', 'INSIGHT', 'VERIFICATION'];
export function typedWord(elapsed: number): string {
  const cycle = 3700;
  const word = words[Math.floor(elapsed / cycle) % words.length];
  const position = elapsed % cycle;
  const typingEnd = Math.max(900, word.length * 90);
  const eraseStart = Math.min(2700, cycle - word.length * 100);
  const visible = position < typingEnd
    ? word.slice(0, Math.floor(position / 90))
    : position < eraseStart
      ? word
      : word.slice(0, Math.max(0, word.length - Math.floor((position - eraseStart) / 100)));
  return visible === word ? `${visible}.` : visible;
}
