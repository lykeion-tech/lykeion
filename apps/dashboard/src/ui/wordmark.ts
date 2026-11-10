// The sidebar wordmark: "Lykeion" is typed in, then alternates with its Greek
// spelling, erasing letter by letter and typing the other word with a flip.
export const WORDS = [Array.from('Lykeion'), Array.from('Λύκειον')];
const LETTERS = 7;
const INTRO_STEP = 80;
const SCRAMBLE = 160;
export const INTRO = (LETTERS - 1) * INTRO_STEP + SCRAMBLE;
export const HOLD = 2500;
const ERASE_STEP = 150;
const TYPE_STEP = 190;
const FLIP = 180;
const ERASE = LETTERS * ERASE_STEP;
const TYPE = (LETTERS - 1) * TYPE_STEP + FLIP;
export const CYCLE = HOLD + ERASE + TYPE;
const POOL = [...WORDS[0]!, ...WORDS[1]!];

export type WordmarkFrame = {
  letters: string[];
  rotation: number[];
};

export function wordmarkFrame(elapsed: number, random = Math.random): WordmarkFrame {
  const flat = Array<number>(LETTERS).fill(0);
  if (elapsed < INTRO) {
    const letters = WORDS[0]!.map((letter, i) => {
      const age = elapsed - i * INTRO_STEP;
      return age < 0 ? '' : age < SCRAMBLE ? POOL[Math.floor(random() * POOL.length)]! : letter;
    });
    return { letters, rotation: flat };
  }
  const since = elapsed - INTRO;
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
