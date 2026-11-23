// The account menu's rows for the other hubs (#1014, lib/hubs.ts). Kept apart from Hubs.tsx so the first screen, which
// draws that menu in the assistant switcher, does not load Settings' hub list and dialogs (and SettingsParts with them,
// #13319).
import { useEffect, useState } from "react";
import { here, hubTitle, loadHubs, onHubsChange, switchURL, type SavedHub } from "../lib/hubs";
import type { MenuItem } from "../ui/atoms";
import { t } from "../i18n";

export function useHubs(): SavedHub[] {
  const [list, setList] = useState(loadHubs);
  useEffect(() => {
    const sync = () => setList(loadHubs());
    // another tab of this hub's page changed it
    const storage = (e: StorageEvent) => e.key === "persona.hubs" && sync();
    addEventListener("storage", storage);
    const off = onHubsChange(sync);
    return () => {
      off();
      removeEventListener("storage", storage);
    };
  }, []);
  return list;
}

/** Open another hub's page, handing it the list. */
export function goToHub(h: SavedHub) {
  location.href = switchURL(h.url, loadHubs());
}

/** The account menu's rows for the other hubs (none when there is no other one). */
export function hubMenuItems(list: SavedHub[]): MenuItem[] {
  return list
    .filter((h) => h.url !== here())
    .map((h, i) => ({ key: `hub:${h.url}`, label: hubTitle(h), hint: t("hubs.switch"), icon: "hub" as const, divider: i === 0, onSelect: () => goToHub(h) }));
}
