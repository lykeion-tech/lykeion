const words = ['DISCOVERY', 'INSIGHT', 'VERIFICATION'];
export function typedWord(elapsed: number): string {
  const cycle = 3700;
  const word = words[Math.floor(elapsed / cycle) % words.length];
  const position = elapsed % cycle;
  const typingEnd = Math.max(900, word.length * 90);
  const eraseStart = Math.min(2700, cycle - word.length * 100);
  if (position < typingEnd) return word.slice(0, Math.floor(position / 90));
  if (position < eraseStart) return word;
  return word.slice(0, Math.max(0, word.length - Math.floor((position - eraseStart) / 100)));
}
