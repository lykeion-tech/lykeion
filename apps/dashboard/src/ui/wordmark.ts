// The sidebar wordmark: it shows "Lykeion" and, after a hold, alternates with its
// Greek spelling, erasing letter by letter and typing the other word with a flip.
// There is no typed intro: the rails remount often and must open on the word.
export const WORDS = [Array.from('Lykeion'), Array.from('Λύκειον')];
const LETTERS = 7;
export const HOLD = 2500;
const ERASE_STEP = 150;
const TYPE_STEP = 190;
const FLIP = 180;
const ERASE = LETTERS * ERASE_STEP;
const TYPE = (LETTERS - 1) * TYPE_STEP + FLIP;
export const CYCLE = HOLD + ERASE + TYPE;

export type WordmarkFrame = {
  letters: string[];
  rotation: number[];
};

export function wordmarkFrame(since: number): WordmarkFrame {
  const flat = Array<number>(LETTERS).fill(0);
  const turn = Math.floor(since / CYCLE);
  const from = WORDS[turn % 2]!;
  const to = WORDS[(turn + 1) % 2]!;
  const t = (since % CYCLE) - HOLD;
  if (t < 0) return { letters: [...from], rotation: flat };
  if (t < ERASE) {
    const left = Math.max(0, LETTERS - Math.floor(t / ERASE_STEP) - 1);
    return {
      letters: from.map((letter, i) => (i < left ? letter : '')),
      rotation: flat,
    };
  }
  const typed = t - ERASE;
  return {
    letters: to.map((letter, i) => (typed >= i * TYPE_STEP ? letter : '')),
    rotation: to.map((_, i) => {
      const age = typed - i * TYPE_STEP;
      return age < 0 ? 0 : -90 * (1 - Math.min(1, age / FLIP));
    }),
  };
}
