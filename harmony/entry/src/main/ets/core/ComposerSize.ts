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

// UIKit body advances change with optical size. Measure the platform's default
// font against the same mixed alphabet, spaces, punctuation and numerals.
// The user's draft always goes through the actual platform paragraph shaper.
export const composerTypeSample = 'The quick brown fox jumps over the lazy dog. 0123456789';
const bodyMetrics: number[][] = [
  [14,15,378.984], [15,16,402.026], [16,17,425.391], [17,19,446.499],
  [19,21,495.604], [21,23,535.766], [23,28,581.675], [28,28,706.945],
  [33,28,829.641], [40,28,1000.254], [47,28,1170.25], [53,28,1315.373]
];

export class ComposerReadingSize {
  readonly points: number;
  readonly shellPoints: number;
  readonly sampleWidth: number;
  constructor(points: number, shellPoints: number, sampleWidth: number) {
    this.points = points;
    this.shellPoints = shellPoints;
    this.sampleWidth = sampleWidth;
  }
  // The measured body face has the same 2444 / 2048 line advance at every category.
  get line(): number { return this.points * 2444 / 2048; }
  height(lines: number): number {
    if (lines === 1) return Math.ceil(Math.max(this.line, this.shellPoints * 2444 / 2048));
    return Math.round(lines * this.line);
  }
}

export function composerReadingSize(fontScale: number): ComposerReadingSize {
  const points = Math.min(53, Math.max(14, 17 * fontScale));
  let upper = 1;
  while (upper < bodyMetrics.length - 1 && points > bodyMetrics[upper][0]) upper++;
  const a = bodyMetrics[upper - 1], b = bodyMetrics[upper];
  const t = (points - a[0]) / (b[0] - a[0]);
  return new ComposerReadingSize(points, a[1] + (b[1] - a[1]) * t, a[2] + (b[2] - a[2]) * t);
}
