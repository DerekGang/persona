/** Fmt.relative on iPhone compares local calendar days after the minute range. */
export function relativeActivityDate(at: number, now: number, locale: string, justNow: string, minutesAgo: string, yesterday: string): string {
  if (!Number.isFinite(at) || at <= 946684800000) return '';
  const seconds = (now - at) / 1000;
  if (seconds < 45) return justNow;
  if (seconds < 3600) return minutesAgo.replace('{n}', String(Math.max(1, Math.floor(seconds / 60))));
  const date = new Date(at);
  const localDay = (value: Date): number => Date.UTC(value.getFullYear(), value.getMonth(), value.getDate()) / 86400000;
  const days = localDay(new Date(now)) - localDay(date);
  if (!days) return date.toLocaleTimeString(locale || undefined, {hour: 'numeric', minute: '2-digit'});
  if (days === 1) return yesterday;
  if (seconds < 6 * 86400) return date.toLocaleDateString(locale || undefined, {weekday: 'long'});
  return date.toLocaleDateString(locale || undefined, {month: 'short', day: 'numeric'});
}

/** A due date always names its weekday. Whole-day deadlines keep their local calendar date. */
export function phoneDueDate(at: number, now: number, locale: string, timed: boolean): string {
  if (!Number.isFinite(at)) return '';
  const date = new Date(at);
  const options: Intl.DateTimeFormatOptions = {weekday: 'short', month: 'short', day: 'numeric'};
  if (date.getFullYear() !== new Date(now).getFullYear()) options.year = 'numeric';
  if (timed) { options.hour = 'numeric'; options.minute = '2-digit'; }
  return new Intl.DateTimeFormat(locale || undefined, options).format(date);
}

/** Elapsed time for a live exchange, starting at its transcript input or wake. */
export function workingTurnLabel(start: number, now: number, translate: (key: string) => string): string {
  if (!Number.isFinite(start) || start <= 0 || !Number.isFinite(now) || now < start) return translate('working');
  const seconds = Math.floor((now - start) / 1000);
  const minutes = Math.floor(seconds / 60);
  if (minutes === 0) return translate('phone_working_seconds').replace('{s}', String(seconds));
  if (minutes >= 60) return translate('phone_working_hours_minutes')
    .replace('{h}', String(Math.floor(minutes / 60))).replace('{m}', String(minutes % 60));
  const pattern = seconds % 60 ? 'phone_working_minutes_seconds' : 'phone_working_minutes';
  return translate(pattern).replace('{m}', String(minutes)).replace('{s}', String(seconds % 60));
}
