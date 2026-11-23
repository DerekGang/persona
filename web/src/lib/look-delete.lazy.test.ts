/// <reference types="node" />
// The avatar picker (views/assistant/Persona.tsx) is on the first screen. It needs only whether the hub makes looks from
// images, deleting one and the words for a refusal: importing them from look-upload.ts put the crop, export and
// upload code of the lazily loaded dialog on every first screen.
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import * as upload from "./look-upload";
import { deleteUploadedLook, LOOK_UPLOADS_FEATURE, refusalText } from "./look-delete";

const read = (file: string) => readFileSync(new URL(file, import.meta.url), "utf8");
const valueImport = (from: string) => new RegExp(`^import\\s+(?!type\\b)[^;]*from\\s+["']${from}["']`, "m");

describe("look-delete", () => {
  it("keeps look-upload.ts off the first screen", () => {
    expect(read("../views/assistant/Persona.tsx")).not.toMatch(valueImport("\\.\\./\\.\\./lib/look-upload"));
    expect(read("./look-delete.ts")).not.toMatch(valueImport("\\./look-upload"));
  });

  it("is what look-upload.ts offers the dialog", () => {
    expect(upload.LOOK_UPLOADS_FEATURE).toBe(LOOK_UPLOADS_FEATURE);
    expect(upload.refusalText).toBe(refusalText);
    expect(upload.deleteUploadedLook).toBe(deleteUploadedLook);
  });
});
