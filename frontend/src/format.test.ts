import { describe, expect, it } from "vitest";

import { fileName, formatBytes, formatClock, formatDuration } from "./format.js";

describe("format helpers", () => {
  it("formats byte units and invalid values defensively", () => {
    expect(formatBytes(1536)).toBe("1.5 KiB");
    expect(formatBytes(Number.NaN)).toBe("0 B");
  });

  it("formats durations and clocks", () => {
    expect(formatDuration(65)).toBe("1m 5s");
    expect(formatClock(3661)).toBe("01:01:01");
  });

  it("extracts filenames across supported platforms", () => {
    expect(fileName("/tmp/disk.img.gz")).toBe("disk.img.gz");
    expect(fileName("C:\\backups\\disk.img.gz")).toBe("disk.img.gz");
  });
});
