// Stands in for web/src/lib/manager-choice.ts (which manager the page shows, kept in localStorage). The demo talks to
// the default manager only.
export const MANAGER_HEADER = "X-Persona-Manager";
export const currentHeaders = (): Record<string, string> => ({});
export const noteItemManager = (_manager?: string) => {};
export const shownManager = (): string => "";
export const onManagerChoice = (_f: () => void) => () => {};
export const useShownManager = (): string => "";
