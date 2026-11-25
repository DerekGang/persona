// @vitest-environment jsdom
// Controlled file cards use only their supplied files, stats, callbacks and URL resolvers.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PathStat } from "../../lib/path-presence";
import type { AttachedFile } from "../../lib/turns";

const mocks = vi.hoisted(() => ({
  fileThumbURL: vi.fn(),
  fileDownloadURL: vi.fn(),
  openPanel: vi.fn(),
  usePathLinks: vi.fn(),
}));

vi.mock("../../lib/api", () => ({
  fileThumbURL: mocks.fileThumbURL,
  fileDownloadURL: mocks.fileDownloadURL,
}));
vi.mock("../../lib/panel", () => ({ openPanel: mocks.openPanel }));
vi.mock("../../lib/path-presence", () => ({ usePathLinks: mocks.usePathLinks }));

const { DocumentCards, FileCards, ReplyFiles } = await import("./FileCards");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const stats: Record<string, PathStat> = {
  "/w/report.docx": { kind: "file", size: 2048 },
  "/w/video.mp4": { kind: "file", size: 4096 },
  "/w/log.txt": { kind: "file", size: 10 },
  "/w/gone.pdf": { kind: "missing" },
};

let host: HTMLDivElement;
let root: Root;

const mount = async (node: React.ReactNode) => {
  await act(async () => root.render(node));
};

const button = (title: string) => {
  const found = [...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.title === title);
  if (!found) throw new Error(`button not found: ${title}`);
  return found;
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal("ResizeObserver", class {
    observe() {}
    unobserve() {}
    disconnect() {}
  });
  mocks.fileThumbURL.mockImplementation((_mid: string, path: string, cwd: string | undefined, size: number) => `default-thumb:${path}:${cwd ?? ""}:${size}`);
  mocks.fileDownloadURL.mockImplementation((_mid: string, path: string, cwd: string | undefined, options: { inline?: boolean } = {}) => `default-download:${path}:${cwd ?? ""}:${options.inline ? "inline" : "attachment"}`);
  mocks.usePathLinks.mockReturnValue({ linkable: () => true, key: "k", stat: (path: string) => stats[path] });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

describe("controlled file cards", () => {
  it("renders explicit reply files without usePathLinks or any default file resource", async () => {
    const files: AttachedFile[] = [
      { kind: "file", path: "/w/log.txt", name: "log.txt", size: 10 },
      { kind: "video", path: "/w/video.mp4", name: "video.mp4", size: 4096 },
      { kind: "file", path: "/w/report.docx", name: "report.docx", size: 2048 },
    ];
    const onOpenFile = vi.fn();
    const resolvePathStat = vi.fn((path: string) => stats[path]);
    const resolveThumbURL = vi.fn((path: string, size: number) => `thumb:${path}:${size}`);
    const resolveDownloadURL = vi.fn((path: string, options: { inline?: boolean }) => `download:${path}:${options.inline ? "inline" : "attachment"}`);

    await mount(
      <ReplyFiles
        text=""
        mid="m1"
        cwd="/w"
        files={files}
        resolvePathStat={resolvePathStat}
        onOpenFile={onOpenFile}
        resolveThumbURL={resolveThumbURL}
        resolveDownloadURL={resolveDownloadURL}
      />,
    );

    expect(mocks.usePathLinks).not.toHaveBeenCalled();
    expect(resolvePathStat).not.toHaveBeenCalled();
    expect(host.querySelector<HTMLImageElement>('[data-testid="document-card"] img')?.getAttribute("src")).toBe("thumb:/w/report.docx:128");
    expect(host.querySelector("video")?.getAttribute("src")).toBe("download:/w/video.mp4:inline");
    expect(mocks.fileThumbURL).not.toHaveBeenCalled();
    expect(mocks.fileDownloadURL).not.toHaveBeenCalled();

    act(() => button("/w/log.txt").click());
    act(() => button("/w/report.docx").click());
    expect(onOpenFile.mock.calls.map(([path]) => path)).toEqual(["/w/log.txt", "/w/report.docx"]);
    expect(mocks.openPanel).not.toHaveBeenCalled();

    act(() => host.querySelector("video")!.dispatchEvent(new Event("error")));
    const download = host.querySelector<HTMLAnchorElement>("a[download]");
    expect(download?.getAttribute("href")).toBe("download:/w/video.mp4:attachment");
    expect(resolveDownloadURL).toHaveBeenCalledWith("/w/video.mp4", {});
  });

  it("uses controlled path stats without mounting usePathLinks", async () => {
    const resolvePathStat = vi.fn((path: string) => stats[path]);
    const resolveThumbURL = vi.fn((path: string, size: number) => `thumb:${path}:${size}`);
    const resolveDownloadURL = vi.fn((path: string, options: { inline?: boolean }) => `download:${path}:${options.inline ? "inline" : "attachment"}`);
    await mount(
      <ReplyFiles
        text={"Wrote `/w/report.docx` and `/w/video.mp4`; `/w/gone.pdf` is missing."}
        mid="m1"
        cwd="/w"
        resolvePathStat={resolvePathStat}
        onOpenFile={() => {}}
        resolveThumbURL={resolveThumbURL}
        resolveDownloadURL={resolveDownloadURL}
      />,
    );

    expect(mocks.usePathLinks).not.toHaveBeenCalled();
    expect(resolvePathStat.mock.calls.map(([path]) => path)).toEqual(["/w/report.docx", "/w/video.mp4", "/w/gone.pdf"]);
    expect(host.textContent).toContain("video.mp4");
    expect(host.textContent).not.toContain("gone.pdf");
    expect(host.querySelector<HTMLImageElement>('[data-testid="document-card"] img')?.getAttribute("src")).toBe("thumb:/w/report.docx:128");
    expect(host.querySelector("video")?.getAttribute("src")).toBe("download:/w/video.mp4:inline");
    expect(mocks.fileThumbURL).not.toHaveBeenCalled();
    expect(mocks.fileDownloadURL).not.toHaveBeenCalled();
    expect(mocks.openPanel).not.toHaveBeenCalled();
  });

  it("uses the supplied cwd with default resources when no resolver is supplied", async () => {
    await mount(<FileCards files={[{ kind: "video", path: "/w/video.mp4", name: "video.mp4" }]} mid="m1" cwd="/panel" />);
    expect(host.querySelector("video")?.getAttribute("src")).toBe("default-download:/w/video.mp4:/panel:inline");
    expect(mocks.fileDownloadURL).toHaveBeenCalledWith("m1", "/w/video.mp4", "/panel", { inline: true });

    await mount(<FileCards files={[{ kind: "file", path: "/w/log.txt", name: "log.txt" }]} mid="m1" cwd="/panel" />);
    act(() => button("/w/log.txt").click());
    expect(mocks.openPanel).toHaveBeenCalledWith({ kind: "file", mid: "m1", path: "/w/log.txt", cwd: "/panel" });
  });

  it("retries a document thumbnail after its manager, cwd or resolver changes", async () => {
    const file: AttachedFile = { kind: "file", path: "/w/report.docx", name: "report.docx" };
    const first = vi.fn(() => "thumb:first");
    const second = vi.fn(() => "thumb:second");
    await mount(<DocumentCards files={[file]} mid="m1" cwd="/one" resolveThumbURL={first} />);
    act(() => host.querySelector("img")!.dispatchEvent(new Event("error")));
    expect(host.querySelector("img")).toBeNull();

    await mount(<DocumentCards files={[file]} mid="m1" cwd="/one" resolveThumbURL={second} />);
    expect(host.querySelector<HTMLImageElement>("img")?.getAttribute("src")).toBe("thumb:second");

    const third = vi.fn(() => "thumb:third");
    await mount(<DocumentCards files={[file]} mid="m2" cwd="/two" resolveThumbURL={third} />);
    expect(host.querySelector<HTMLImageElement>("img")?.getAttribute("src")).toBe("thumb:third");
  });

  it("retries failed media and clears the previous duration when the resource scope changes", async () => {
    const file: AttachedFile = { kind: "video", path: "/w/video.mp4", name: "video.mp4", duration: 10 };
    const first = vi.fn((path: string, options: { inline?: boolean }) => `download:first:${path}:${options.inline ? "inline" : "attachment"}`);
    const second = vi.fn((path: string, options: { inline?: boolean }) => `download:second:${path}:${options.inline ? "inline" : "attachment"}`);
    await mount(<FileCards files={[file]} mid="m1" cwd="/one" resolveDownloadURL={first} />);
    const video = host.querySelector("video")!;
    Object.defineProperty(video, "duration", { configurable: true, value: 65 });
    act(() => video.dispatchEvent(new Event("loadedmetadata")));
    expect(host.textContent).toContain("1:05");

    act(() => host.querySelector("video")!.dispatchEvent(new Event("error")));
    expect(host.querySelector("video")).toBeNull();
    expect(host.querySelector("a[download]")?.getAttribute("href")).toContain("attachment");

    await mount(<FileCards files={[file]} mid="m1" cwd="/one" resolveDownloadURL={second} />);
    expect(host.querySelector("video")?.getAttribute("src")).toBe("download:second:/w/video.mp4:inline");
    expect(host.textContent).toContain("0:10");
    expect(host.textContent).not.toContain("1:05");
  });

  it("draws a controlled reply file when its path stat arrives after the first render", async () => {
    const text = "Wrote `/w/报告.docx`.";
    let stat: PathStat | undefined;
    const resolvePathStat = vi.fn(() => stat);
    const resolveThumbURL = vi.fn(() => "thumb:report");
    await mount(<ReplyFiles text={text} mid="m1" cwd="/w" resolvePathStat={resolvePathStat} resolveThumbURL={resolveThumbURL} />);
    expect(host.querySelector('[data-testid="document-card"]')).toBeNull();

    stat = { kind: "file", size: 2048 };
    await mount(<ReplyFiles text={text} mid="m1" cwd="/w" resolvePathStat={resolvePathStat} resolveThumbURL={resolveThumbURL} />);
    expect(host.querySelector('[data-testid="document-card"]')).not.toBeNull();
  });
});

describe("default file card compatibility", () => {
  it("keeps the default thumbnail, open panel and path-presence behavior", async () => {
    await mount(<DocumentCards files={[{ kind: "file", path: "/w/report.docx", name: "report.docx" }]} mid="m1" cwd="/w" />);
    expect(host.querySelector<HTMLImageElement>('[data-testid="document-card"] img')?.getAttribute("src")).toBe("default-thumb:/w/report.docx:/w:128");
    act(() => button("/w/report.docx").click());
    expect(mocks.openPanel).toHaveBeenCalledWith({ kind: "file", mid: "m1", path: "/w/report.docx" });

    await mount(<ReplyFiles text={"Wrote `/w/report.docx`."} mid="m1" cwd="/w" />);
    expect(mocks.usePathLinks).toHaveBeenCalledWith("m1", "/w", ["/w/report.docx"]);
    expect(host.textContent).toContain("report.docx");
    expect(mocks.fileThumbURL).toHaveBeenCalledWith("m1", "/w/report.docx", "/w", 128);
  });

  it("keeps the default panel object for a file card without cwd", async () => {
    await mount(<FileCards files={[{ kind: "file", path: "/w/log.txt", name: "log.txt" }]} mid="m1" />);
    act(() => button("/w/log.txt").click());
    expect(mocks.openPanel).toHaveBeenCalledWith({ kind: "file", mid: "m1", path: "/w/log.txt" });
  });
});
