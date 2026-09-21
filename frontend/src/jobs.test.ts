import { describe, expect, it } from "vitest";

import type { Job } from "../bindings/github.com/meltingcore/malina/internal/core/models.js";
import { jobFraction, updateJobRuntime } from "./jobs.js";

const job = (overrides: Partial<Job> = {}): Job => ({
  id: "BKP-0001",
  type: "backup",
  title: "Backup",
  subtitle: "Test",
  status: "running",
  phase: "backup",
  message: "Streaming",
  startedAt: "2026-09-21T00:00:00Z",
  ...overrides,
});

describe("job state helpers", () => {
  it("clamps reported progress", () => {
    expect(jobFraction(job({ fraction: 2 }))).toBe(1);
    expect(jobFraction(job({ bytes: 25, totalBytes: 100, fraction: undefined }))).toBe(0.25);
  });

  it("calculates throughput and resets it when the phase changes", () => {
    const initial = updateJobRuntime(undefined, job({ bytes: 100 }), 1_000);
    const streaming = updateJobRuntime(initial, job({ bytes: 1_100 }), 2_000);
    expect(streaming.rate).toBe(1_000);

    const verifying = updateJobRuntime(streaming, job({ phase: "readback", bytes: 0 }), 3_000);
    expect(verifying.rate).toBe(0);
    expect(verifying.lastBytes).toBe(0);
  });

  it("does not report throughput for paused jobs", () => {
    const initial = updateJobRuntime(undefined, job({ bytes: 0 }), 1_000);
    const paused = updateJobRuntime(initial, job({ status: "paused", bytes: 1_000 }), 2_000);
    expect(paused.rate).toBe(0);
  });
});
