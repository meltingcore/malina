import type { Job } from "../bindings/github.com/meltingcore/malina/internal/core/models.js";

export type JobFilter = "all" | "active" | "completed";

export type JobRuntime = {
  job: Job;
  rate: number;
  lastBytes: number;
  lastAt: number;
  phase: string;
};

export const isActiveJob = (job: Job): boolean => (
  job.status === "running" || job.status === "paused" || job.status === "cancelling"
);

export const jobFraction = (job: Job): number => {
  if (job.status === "completed") return 1;
  const calculated = (job.totalBytes ?? 0) > 0 ? (job.bytes ?? 0) / (job.totalBytes ?? 1) : 0;
  return Math.max(0, Math.min(1, job.fraction ?? calculated));
};

export const statusText = (job: Job): string => {
  if (job.status === "running") {
    const phase = job.phase === "readback" ? "Verifying" : job.phase === "restore" ? "Restoring" : job.phase === "backup" ? "Backing up" : "Running";
    const percent = Math.round(jobFraction(job) * 100);
    return percent > 0 ? `${phase} ${percent}%` : phase;
  }
  return job.status.charAt(0).toUpperCase() + job.status.slice(1);
};

export const elapsedSeconds = (job: Job, now = Date.now()): number => {
  const start = Date.parse(job.startedAt);
  const end = job.completedAt ? Date.parse(job.completedAt) : now;
  return Number.isFinite(start) && Number.isFinite(end) ? Math.max(0, (end - start) / 1000) : 0;
};

export const updateJobRuntime = (previous: JobRuntime | undefined, job: Job, now: number): JobRuntime => {
  const bytes = job.bytes ?? 0;
  let rate = previous?.rate ?? 0;
  let lastBytes = previous?.lastBytes ?? bytes;
  let lastAt = previous?.lastAt ?? now;
  if (previous && previous.phase !== job.phase) {
    rate = 0;
    lastBytes = bytes;
    lastAt = now;
  } else if (previous && bytes > lastBytes && now > lastAt) {
    const instantRate = (bytes - lastBytes) / ((now - lastAt) / 1000);
    rate = rate > 0 ? rate * 0.68 + instantRate * 0.32 : instantRate;
    lastBytes = bytes;
    lastAt = now;
  }
  if (!isActiveJob(job) || job.status === "paused") rate = 0;
  return { job, rate, lastBytes, lastAt, phase: job.phase };
};
