// What the avatar picker needs on the first screen for uploaded looks (#2404, docs/apple/persona.md "Uploading an image"):
// whether the hub makes them, deleting one, and the words for a refusal. Making one (the crop, the export and the
// upload) is lib/look-upload.ts, which only the lazily loaded dialog (views/assistant/LookUpload.tsx) needs.
import { ApiError, api, enc, errorText } from "./api";
import { compatibleEnvelope } from "./compatibility";
import type { LookCurrent } from "./persona";

/** hello / health `features`: the hub makes avatars from uploaded images. Without it the picker offers no upload. */
export const LOOK_UPLOADS_FEATURE = "look_uploads";

/** What to tell the user about a failed request: the hub's own words (already in their language) as they are. */
export function refusalText(e: unknown): string {
  if (e instanceof ApiError && e.isJson && typeof e.body.error === "string" && e.body.error.trim()) return e.body.error;
  return errorText(e);
}

/** A choice as the hub sent it, or undefined when the answer has none. */
export function currentOf(cur: LookCurrent | undefined): LookCurrent | undefined {
  return cur && typeof cur === "object" && typeof cur.effective === "string" ? compatibleEnvelope(cur) : undefined;
}

/** Deletes an uploaded look; the answer is the choice after it (the default when it was the chosen one). */
export async function deleteUploadedLook(id: string): Promise<LookCurrent | undefined> {
  const r = await api<{ current?: LookCurrent }>(`/looks/uploads/${enc(id)}`, { method: "DELETE" });
  return currentOf(r.current);
}
