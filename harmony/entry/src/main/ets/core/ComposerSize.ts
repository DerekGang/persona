/** Resting phone controls follow the shell's type size, one category above the capped interface. */
export class ComposerSize {
  readonly target: number;
  readonly plus: number;
  readonly microphone: number;
  constructor(target: number, plus: number, microphone: number) {
    this.target = target;
    this.plus = plus;
    this.microphone = microphone;
  }
}

// The columns are body points at the system category, then the shell category's
// UIFontMetrics-scaled 17pt value, title2 and headline. The interface stops at xxxLarge.
const steps: number[][] = [
  [14, 46 / 3, 20, 15], [15, 49 / 3, 21, 16], [16, 17, 22, 17], [17, 56 / 3, 24, 19],
  [19, 20, 26, 21], [21, 67 / 3, 28, 23], [23, 79 / 3, 34, 28]
];

export function restingComposerSize(fontScale: number): ComposerSize {
  const points = Math.min(23, Math.max(14, 17 * fontScale));
  let upper = 1;
  while (upper < steps.length - 1 && points > steps[upper][0]) upper++;
  const a = steps[upper - 1], b = steps[upper];
  const fraction = (points - a[0]) / (b[0] - a[0]);
  const metric = a[1] + fraction * (b[1] - a[1]);
  const title = a[2] + fraction * (b[2] - a[2]);
  const headline = a[3] + fraction * (b[3] - a[3]);
  // At the standard shell size, a 24vp Harmony microphone gives the 19pt iPhone headline icon's painted height.
  return new ComposerSize(44 * metric / 17, title, headline * 24 / 19);
}
