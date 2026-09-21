export const formatBytes = (bytes: number): string => {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  const unit = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  return `${(bytes / 1024 ** unit).toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
};

export const formatDuration = (seconds: number): string => {
  if (!Number.isFinite(seconds) || seconds < 0) return "Calculating…";
  const rounded = Math.max(0, Math.round(seconds));
  const hours = Math.floor(rounded / 3600);
  const minutes = Math.floor((rounded % 3600) / 60);
  const remainingSeconds = rounded % 60;
  if (hours > 0) return `${hours}h ${minutes}m`;
  if (minutes > 0) return `${minutes}m ${remainingSeconds}s`;
  return `${remainingSeconds}s`;
};

export const formatClock = (seconds: number): string => {
  const rounded = Math.max(0, Math.floor(seconds));
  const hours = Math.floor(rounded / 3600);
  const minutes = Math.floor((rounded % 3600) / 60);
  const remainingSeconds = rounded % 60;
  const clock = `${String(minutes).padStart(2, "0")}:${String(remainingSeconds).padStart(2, "0")}`;
  return hours > 0 ? `${String(hours).padStart(2, "0")}:${clock}` : clock;
};

export const formatRate = (bytesPerSecond: number): string => (
  bytesPerSecond > 0 ? `${formatBytes(bytesPerSecond)}/s` : "Waiting for data"
);

export const fileName = (path: string): string => path.split(/[\\/]/).pop() || path;
