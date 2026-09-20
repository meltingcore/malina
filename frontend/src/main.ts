import { CancelError, Events } from "@wailsio/runtime";

import type { Backup, Connection, Device, Job, PiInfo } from "../bindings/github.com/meltingcore/malina/internal/core/models.js";
import * as Service from "../bindings/github.com/meltingcore/malina/internal/desktop/service.js";
import "./style.css";

type JobFilter = "all" | "active" | "completed";
type JobRuntime = { job: Job; rate: number; lastBytes: number; lastAt: number; phase: string };

const element = <T extends HTMLElement>(id: string): T => {
  const found = document.getElementById(id);
  if (!found) throw new Error(`Missing required element: ${id}`);
  return found as T;
};

const backupForm = element<HTMLFormElement>("backup-form");
const restoreForm = element<HTMLFormElement>("restore-form");
const hostInput = element<HTMLInputElement>("host");
const portInput = element<HTMLInputElement>("port");
const passwordInput = element<HTMLInputElement>("password");
const inspectButton = element<HTMLButtonElement>("inspect-pi");
const cancelInspectButton = element<HTMLButtonElement>("cancel-inspect");
const identityDisplay = element<HTMLElement>("identity-display");
const outputDisplay = element<HTMLElement>("output-display");
const piResult = element<HTMLDivElement>("pi-result");
const togglePasswordButton = element<HTMLButtonElement>("toggle-password");
const selectedBackupElement = element<HTMLDivElement>("selected-backup");
const backupReady = element<HTMLElement>("backup-ready");
const backupChecksum = element<HTMLElement>("backup-checksum");
const deviceSelect = element<HTMLSelectElement>("device");
const targetDetail = element<HTMLElement>("target-detail");
const restoreWarning = element<HTMLElement>("restore-warning");
const verifyWriteInput = element<HTMLInputElement>("verify-write");
const restoreButton = element<HTMLButtonElement>("restore-button");
const restoreConfirmOverlay = element<HTMLElement>("restore-confirm-overlay");
const restoreConfirmImage = element<HTMLElement>("restore-confirm-image");
const restoreConfirmTarget = element<HTMLElement>("restore-confirm-target");
const cancelRestoreConfirmButton = element<HTMLButtonElement>("cancel-restore-confirm");
const confirmRestoreButton = element<HTMLButtonElement>("confirm-restore");
const operation = element<HTMLElement>("operation");
const operationTitle = element<HTMLElement>("operation-title");
const operationDetail = element<HTMLElement>("operation-detail");
const operationProgress = element<HTMLProgressElement>("operation-progress");
const operationClose = element<HTMLButtonElement>("operation-close");
const jobsCount = element<HTMLElement>("jobs-count");
const jobsSummary = element<HTMLElement>("jobs-summary");
const jobsList = element<HTMLElement>("jobs-list");
const jobsEmpty = element<HTMLElement>("jobs-empty");
const allJobCount = element<HTMLElement>("all-job-count");
const activeJobCount = element<HTMLElement>("active-job-count");
const finishedJobCount = element<HTMLElement>("finished-job-count");
const jobSearch = element<HTMLInputElement>("job-search");
const jobDetailsOverlay = element<HTMLElement>("job-details-overlay");
const livePanel = element<HTMLElement>("live-panel");
const progressSource = element<HTMLElement>("progress-source");
const progressDestination = element<HTMLElement>("progress-destination");
const progressStage = element<HTMLElement>("progress-stage");
const streamState = element<HTMLElement>("stream-state");
const progressPercent = element<HTMLElement>("progress-percent");
const progressRate = element<HTMLElement>("progress-rate");
const progressETA = element<HTMLElement>("progress-eta");
const progressTrack = element<HTMLElement>("progress-track");
const progressTrackFill = element<HTMLElement>("progress-track-fill");
const progressByteDetail = element<HTMLElement>("progress-byte-detail");
const metricTransferred = element<HTMLElement>("metric-transferred");
const metricTotal = element<HTMLElement>("metric-total");
const metricThroughput = element<HTMLElement>("metric-throughput");
const metricModeLabel = element<HTMLElement>("metric-mode-label");
const metricMode = element<HTMLElement>("metric-mode");
const metricModeDetail = element<HTMLElement>("metric-mode-detail");
const metricAccessLabel = element<HTMLElement>("metric-access-label");
const metricAccess = element<HTMLElement>("metric-access");
const metricAccessDetail = element<HTMLElement>("metric-access-detail");
const activityTime = element<HTMLElement>("activity-time");
const activityPrimary = element<HTMLElement>("activity-primary");
const activitySecondary = element<HTMLElement>("activity-secondary");
const activityBytes = element<HTMLElement>("activity-bytes");
const progressFooterCopy = element<HTMLElement>("progress-footer-copy");
const pauseButton = element<HTMLButtonElement>("pause-job");
const pauseLabel = element<HTMLElement>("pause-label");
const backgroundButton = element<HTMLButtonElement>("background-job");
const cancelButton = element<HTMLButtonElement>("cancel-job");
const cancelLabel = element<HTMLElement>("cancel-label");
const cancelConfirm = element<HTMLElement>("cancel-confirm");
const cancelConfirmTitle = element<HTMLElement>("cancel-confirm-title");
const cancelConfirmCopy = element<HTMLElement>("cancel-confirm-copy");
const keepJobButton = element<HTMLButtonElement>("keep-job");
const confirmCancelButton = element<HTMLButtonElement>("confirm-cancel-job");
const helpButton = element<HTMLButtonElement>("help-button");
const helpOverlay = element<HTMLElement>("help-overlay");
const closeHelpButton = element<HTMLButtonElement>("close-help");

let identityPath = "";
let outputDirectory = "";
let selectedBackup: Backup | null = null;
let availableDevices: Device[] = [];
let devicesLoaded = false;
let busy = false;
let connectionCheck: ReturnType<typeof Service.Inspect> | null = null;
let currentJobFilter: JobFilter = "all";
let selectedJobID = "";
const jobs = new Map<string, JobRuntime>();

const formatBytes = (bytes: number): string => {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  const unit = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  return `${(bytes / 1024 ** unit).toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
};

const formatDuration = (seconds: number): string => {
  if (!Number.isFinite(seconds) || seconds < 0) return "Calculating…";
  const rounded = Math.max(0, Math.round(seconds));
  const hours = Math.floor(rounded / 3600);
  const minutes = Math.floor((rounded % 3600) / 60);
  const remainingSeconds = rounded % 60;
  if (hours > 0) return `${hours}h ${minutes}m`;
  if (minutes > 0) return `${minutes}m ${remainingSeconds}s`;
  return `${remainingSeconds}s`;
};

const formatClock = (seconds: number): string => {
  const rounded = Math.max(0, Math.floor(seconds));
  const hours = Math.floor(rounded / 3600);
  const minutes = Math.floor((rounded % 3600) / 60);
  const remainingSeconds = rounded % 60;
  const clock = `${String(minutes).padStart(2, "0")}:${String(remainingSeconds).padStart(2, "0")}`;
  return hours > 0 ? `${String(hours).padStart(2, "0")}:${clock}` : clock;
};

const formatRate = (bytesPerSecond: number): string => bytesPerSecond > 0 ? `${formatBytes(bytesPerSecond)}/s` : "Waiting for data";
const fileName = (path: string): string => path.split(/[\\/]/).pop() || path;

const errorMessage = (error: unknown): string => {
  if (error instanceof Error) return error.message;
  if (typeof error === "string") return error;
  if (error && typeof error === "object" && "message" in error) return String(error.message);
  return "The operation failed unexpectedly.";
};

const isActiveJob = (job: Job): boolean => job.status === "running" || job.status === "paused" || job.status === "cancelling";

const jobFraction = (job: Job): number => {
  if (job.status === "completed") return 1;
  const calculated = (job.totalBytes ?? 0) > 0 ? (job.bytes ?? 0) / (job.totalBytes ?? 1) : 0;
  return Math.max(0, Math.min(1, job.fraction ?? calculated));
};

const statusText = (job: Job): string => {
  if (job.status === "running") {
    const phase = job.phase === "readback" ? "Verifying" : job.phase === "restore" ? "Restoring" : job.phase === "backup" ? "Backing up" : "Running";
    const percent = Math.round(jobFraction(job) * 100);
    return percent > 0 ? `${phase} ${percent}%` : phase;
  }
  return job.status.charAt(0).toUpperCase() + job.status.slice(1);
};

const elapsedSeconds = (job: Job): number => {
  const start = Date.parse(job.startedAt);
  const end = job.completedAt ? Date.parse(job.completedAt) : Date.now();
  return Number.isFinite(start) && Number.isFinite(end) ? Math.max(0, (end - start) / 1000) : 0;
};

const authMethod = (): "key" | "password" => {
  const selected = document.querySelector<HTMLInputElement>('input[name="auth-method"]:checked');
  return selected?.value === "password" ? "password" : "key";
};

const connection = (): Connection => {
  const host = hostInput.value.trim();
  const port = Number(portInput.value);
  if (!host) throw new Error("Enter the Pi's SSH address.");
  if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error("Enter a valid SSH port.");
  if (authMethod() === "password" && !passwordInput.value) throw new Error("Enter the SSH password.");
  return {
    host,
    port,
    identity: authMethod() === "key" ? identityPath || undefined : undefined,
    password: authMethod() === "password" ? passwordInput.value : undefined,
  };
};

const updateRestoreAvailability = (): void => {
  restoreButton.disabled = busy || !selectedBackup || !deviceSelect.value;
};

const setBusy = (value: boolean): void => {
  busy = value;
  document.querySelectorAll<HTMLButtonElement>(".action-button").forEach((button) => { button.disabled = value; });
  updateRestoreAvailability();
};

const showError = (error: unknown): void => {
  operation.classList.remove("hidden", "complete");
  operation.classList.add("failed");
  operationTitle.textContent = "Could not complete the operation";
  operationDetail.textContent = errorMessage(error);
  operationProgress.removeAttribute("value");
};

const runOperation = async (task: () => Promise<void>): Promise<void> => {
  if (busy) return;
  setBusy(true);
  try {
    await task();
  } catch (error) {
    showError(error);
  } finally {
    setBusy(false);
  }
};

operationClose.addEventListener("click", () => {
  operation.classList.add("hidden");
});

const openHelp = (): void => {
  helpOverlay.hidden = false;
  helpOverlay.classList.remove("hidden");
  closeHelpButton.focus();
};

const closeHelp = (): void => {
  helpOverlay.classList.add("hidden");
  helpOverlay.hidden = true;
  helpButton.focus();
};

helpButton.addEventListener("click", openHelp);
closeHelpButton.addEventListener("click", closeHelp);
helpOverlay.addEventListener("click", (event) => {
  if (event.target === helpOverlay) closeHelp();
});

const describePi = (info: PiInfo): string => {
  const warning = info.warnings?.length ? ` ${info.warnings.join(" ")}` : "";
  return `${info.supported ? "Ready" : "Unavailable"} · ${info.hostname} · ${formatBytes(info.diskSize)} · ${info.rootDisk}.${warning}`;
};

const setConnectionStatus = (info: PiInfo): void => {
  piResult.className = `connection-status ${info.supported ? "success" : "error"}`;
  const dot = document.createElement("span");
  dot.className = "mini-dot";
  dot.setAttribute("aria-hidden", "true");
  const copy = document.createElement("span");
  copy.textContent = describePi(info);
  piResult.replaceChildren(dot, copy);
};

const resetConnectionStatus = (): void => {
  piResult.className = "connection-status muted-status";
  const dot = document.createElement("span");
  dot.className = "mini-dot";
  dot.setAttribute("aria-hidden", "true");
  const copy = document.createElement("span");
  copy.textContent = "Test the connection to identify the source disk.";
  piResult.replaceChildren(dot, copy);
};

const setConnectionCheckActive = (active: boolean): void => {
  inspectButton.classList.toggle("hidden", active);
  cancelInspectButton.classList.toggle("hidden", !active);
  cancelInspectButton.disabled = false;
};

const setConnectionMessage = (message: string): void => {
  piResult.className = "connection-status muted-status";
  const dot = document.createElement("span");
  dot.className = "mini-dot";
  dot.setAttribute("aria-hidden", "true");
  const copy = document.createElement("span");
  copy.textContent = message;
  piResult.replaceChildren(dot, copy);
};

const setConnectionError = (error: unknown): void => {
  piResult.className = "connection-status error";
  const dot = document.createElement("span");
  dot.className = "mini-dot";
  dot.setAttribute("aria-hidden", "true");
  const copy = document.createElement("span");
  copy.textContent = errorMessage(error);
  piResult.replaceChildren(dot, copy);
};

const renderSelectedBackup = (): void => {
  selectedBackupElement.replaceChildren();
  if (!selectedBackup) {
    selectedBackupElement.className = "picker-value selected-backup empty-selection";
    const label = document.createElement("span");
    label.className = "picker-label";
    label.textContent = "Malina backup folder";
    const title = document.createElement("strong");
    title.className = "backup-title";
    title.textContent = "Choose a backup image";
    const meta = document.createElement("small");
    meta.className = "backup-meta";
    meta.textContent = "No image selected";
    selectedBackupElement.append(label, title, meta);
    backupReady.classList.add("hidden");
    backupChecksum.textContent = "SHA-256 checked before write";
    updateRestoreAvailability();
    return;
  }
  selectedBackupElement.className = "picker-value selected-backup";
  const label = document.createElement("span");
  label.className = "picker-label";
  label.textContent = "Malina backup folder";
  const title = document.createElement("strong");
  title.className = "backup-title";
  title.textContent = fileName(selectedBackup.path);
  title.title = selectedBackup.path;
  const meta = document.createElement("small");
  meta.className = "backup-meta";
  meta.textContent = `${selectedBackup.manifest.source.hostname} · ${new Date(selectedBackup.manifest.createdAt).toLocaleString()} · ${formatBytes(selectedBackup.manifest.image.rawBytes)}`;
  selectedBackupElement.append(label, title, meta);
  backupReady.classList.remove("hidden");
  backupChecksum.textContent = "Manifest loaded";
  updateRestoreAvailability();
};

const renderDevices = (devices: Device[]): void => {
  availableDevices = devices;
  const previous = deviceSelect.value;
  deviceSelect.replaceChildren(new Option(devices.length ? "Choose a drive" : "No removable drives found", ""));
  for (const device of devices) deviceSelect.add(new Option(`${device.path} — ${device.name} (${formatBytes(device.bytes)})`, device.path));
  if (devices.some((device) => device.path === previous)) deviceSelect.value = previous;
  devicesLoaded = true;
  updateTargetSummary();
  updateRestoreAvailability();
};

const updateTargetSummary = (): void => {
  const selected = availableDevices.find((device) => device.path === deviceSelect.value);
  targetDetail.textContent = selected ? `${selected.name} · ${formatBytes(selected.bytes)}` : "No target selected";
  restoreWarning.textContent = selected ? `${selected.path} and all its partitions will be overwritten.` : "The selected drive and all its partitions will be overwritten.";
};

const refreshDevices = async (): Promise<void> => { renderDevices((await Service.ListDevices()) ?? []); };

const switchView = (viewID: string): void => {
  document.querySelectorAll<HTMLButtonElement>(".tab").forEach((tab) => {
    const active = tab.dataset.view === viewID;
    tab.classList.toggle("active", active);
    tab.setAttribute("aria-selected", String(active));
  });
  document.querySelectorAll<HTMLElement>(".view").forEach((view) => {
    const active = view.id === viewID;
    view.classList.toggle("active", active);
    view.hidden = !active;
  });
  if (viewID === "restore-view" && !devicesLoaded) void runOperation(refreshDevices);
};

const routeText = (job: Job): string => {
  const source = job.source ? (job.type === "restore" ? fileName(job.source) : job.source) : "Preparing source";
  const destination = job.destination ? (job.type === "backup" ? fileName(job.destination) : job.destination) : "Preparing destination";
  return `${source} → ${destination}`;
};

const upsertJob = (job: Job): void => {
  const now = performance.now();
  const previous = jobs.get(job.id);
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
  jobs.set(job.id, { job, rate, lastBytes, lastAt, phase: job.phase });
  renderJobs();
  if (selectedJobID === job.id && !jobDetailsOverlay.hidden) renderJobDetails();
};

const jobStat = (runtime: JobRuntime): { primary: string; secondary: string } => {
  const { job, rate } = runtime;
  if (job.status === "completed") return { primary: job.type === "backup" ? "Backed up" : "Restored", secondary: `Duration ${formatDuration(elapsedSeconds(job))}` };
  if (job.status === "failed") return { primary: "Failed", secondary: job.error || "Review details" };
  if (job.status === "cancelled") return { primary: "Cancelled", secondary: `After ${formatDuration(elapsedSeconds(job))}` };
  if (job.status === "paused") return { primary: "Paused", secondary: `${Math.round(jobFraction(job) * 100)}% complete` };
  if (job.status === "cancelling") return { primary: "Stopping…", secondary: "Cleaning up" };
  const remaining = rate > 0 && (job.totalBytes ?? 0) > (job.bytes ?? 0) ? formatDuration(((job.totalBytes ?? 0) - (job.bytes ?? 0)) / rate) : "Calculating…";
  return { primary: rate > 0 ? formatRate(rate) : "Starting…", secondary: `${remaining} left` };
};

const createJobCard = (): HTMLElement => {
  const card = document.createElement("article");
  card.className = "job-card";
  card.tabIndex = 0;

  const icon = document.createElement("span");
  icon.className = "job-kind-icon";
  icon.setAttribute("aria-hidden", "true");

  const copy = document.createElement("div");
  copy.className = "job-copy";
  const tags = document.createElement("div");
  tags.className = "job-tags";
  const typeTag = document.createElement("span");
  typeTag.className = "job-type-tag";
  const idTag = document.createElement("span");
  idTag.className = "job-id-tag";
  const statusTag = document.createElement("span");
  statusTag.className = "job-status-tag";
  tags.append(typeTag, idTag, statusTag);
  const title = document.createElement("h2");
  const subtitle = document.createElement("p");
  copy.append(tags, title, subtitle);

  const stat = document.createElement("div");
  stat.className = "job-stat";
  stat.append(document.createElement("strong"), document.createElement("span"));

  const details = document.createElement("button");
  details.className = "job-details-button";
  details.type = "button";
  details.textContent = "Details ↗";

  card.append(icon, copy, stat, details);
  return card;
};

const renderJobs = (): void => {
  const sorted = Array.from(jobs.values()).sort((a, b) => Date.parse(b.job.startedAt) - Date.parse(a.job.startedAt));
  const active = sorted.filter(({ job }) => isActiveJob(job));
  const finished = sorted.length - active.length;
  allJobCount.textContent = String(sorted.length);
  activeJobCount.textContent = String(active.length);
  finishedJobCount.textContent = String(finished);
  jobsCount.textContent = String(active.length);
  jobsCount.classList.toggle("hidden", active.length === 0);
  jobsSummary.classList.toggle("active", active.length > 0);
  const summaryCopy = jobsSummary.lastChild;
  if (summaryCopy) summaryCopy.textContent = active.length === 0 ? " No active jobs" : ` ${active.length} active ${active.length === 1 ? "job" : "jobs"}`;

  const query = jobSearch.value.trim().toLocaleLowerCase();
  const visible = sorted.filter(({ job }) => {
    const matchesFilter = currentJobFilter === "all" || (currentJobFilter === "active" ? isActiveJob(job) : !isActiveJob(job));
    const searchable = `${job.id} ${job.type} ${job.title} ${job.subtitle} ${job.source ?? ""} ${job.destination ?? ""}`.toLocaleLowerCase();
    return matchesFilter && (!query || searchable.includes(query));
  }).slice(0, 4);

  jobsEmpty.classList.toggle("hidden", visible.length > 0);
  if (visible.length === 0) {
    jobsList.replaceChildren();
    const emptyTitle = jobsEmpty.querySelector("strong");
    const emptyCopy = jobsEmpty.querySelector("span:last-child");
    if (emptyTitle) emptyTitle.textContent = sorted.length ? "No matching jobs" : "No jobs yet";
    if (emptyCopy) emptyCopy.textContent = sorted.length ? "Try a different filter or search." : "Start a backup or restore and it will appear here.";
    return;
  }

  const existingCards = new Map(
    Array.from(jobsList.querySelectorAll<HTMLElement>(".job-card"))
      .map((card) => [card.dataset.jobId ?? "", card]),
  );
  const visibleIDs = new Set<string>();

  visible.forEach((runtime, index) => {
    const { job } = runtime;
    visibleIDs.add(job.id);
    const card = existingCards.get(job.id) ?? createJobCard();
    card.className = `job-card ${job.type}${selectedJobID === job.id ? " selected" : ""}`;
    card.dataset.jobId = job.id;
    const icon = card.querySelector<HTMLElement>(".job-kind-icon")!;
    icon.textContent = job.type === "backup" ? "↥" : "↯";
    const typeTag = card.querySelector<HTMLElement>(".job-type-tag")!;
    typeTag.textContent = job.type;
    const idTag = card.querySelector<HTMLElement>(".job-id-tag")!;
    idTag.textContent = `#${job.id}`;
    const statusTag = card.querySelector<HTMLElement>(".job-status-tag")!;
    statusTag.className = `job-status-tag ${job.status}`;
    statusTag.textContent = statusText(job);
    const title = card.querySelector<HTMLElement>(".job-copy h2")!;
    title.textContent = job.title;
    const subtitle = card.querySelector<HTMLElement>(".job-copy p")!;
    subtitle.textContent = routeText(job);
    const statValue = jobStat(runtime);
    const primary = card.querySelector<HTMLElement>(".job-stat strong")!;
    primary.textContent = statValue.primary;
    const secondary = card.querySelector<HTMLElement>(".job-stat span")!;
    secondary.textContent = statValue.secondary;

    const currentAtIndex = jobsList.children.item(index);
    if (currentAtIndex !== card) jobsList.insertBefore(card, currentAtIndex);
  });

  existingCards.forEach((card, id) => {
    if (!visibleIDs.has(id)) card.remove();
  });
};

const setPipelineState = (label: string, stateClass = ""): void => {
  streamState.className = `stream-state ${stateClass}`.trim();
  const dot = document.createElement("span");
  dot.setAttribute("aria-hidden", "true");
  streamState.replaceChildren(dot, document.createTextNode(label));
};

const renderJobDetails = (): void => {
  const runtime = jobs.get(selectedJobID);
  if (!runtime) return;
  const { job, rate } = runtime;
  const active = isActiveJob(job);
  const fraction = jobFraction(job);
  const percent = Math.round(fraction * 100);
  const bytes = job.bytes ?? 0;
  const total = job.totalBytes ?? 0;
  const isBackup = job.type === "backup";
  const stateClass = job.status === "failed" ? "failed" : job.status === "cancelled" || job.status === "cancelling" ? "cancelled" : job.status === "paused" ? "paused" : "";
  const state = job.status === "running" ? (job.phase === "readback" ? "Verifying" : job.phase === "restore" ? "Writing" : job.phase === "backup" ? "Streaming" : "Preparing") : statusText(job);
  livePanel.className = `live-panel ${job.status}`;
  setPipelineState(state, stateClass);
  progressSource.textContent = job.source ? (isBackup ? job.source : fileName(job.source)) : "Preparing source";
  progressDestination.textContent = job.destination ? (isBackup ? fileName(job.destination) : job.destination) : "Preparing destination";
  progressStage.textContent = job.error || job.message || "Preparing job";
  progressPercent.textContent = `${percent}%`;
  progressTrackFill.style.width = `${percent}%`;
  progressTrack.setAttribute("aria-valuenow", String(percent));
  progressTrack.setAttribute("aria-label", `${isBackup ? "Backup" : "Restore"} progress`);
  progressRate.textContent = job.status === "paused" ? "Stream paused" : active ? formatRate(rate) : statusText(job);
  progressETA.textContent = job.status === "paused" ? "Paused" : job.status === "cancelling" ? "Stopping…" : !active ? (job.status === "completed" ? "Complete" : "Stopped") : rate > 0 && total > bytes ? formatDuration((total - bytes) / rate) : "Calculating…";
  progressByteDetail.textContent = total > 0 ? `${formatBytes(bytes)} / ${formatBytes(total)}` : `${formatBytes(bytes)} transferred`;
  metricTransferred.textContent = formatBytes(bytes);
  metricTotal.textContent = total > 0 ? `of ${formatBytes(total)}` : "of —";
  metricThroughput.textContent = job.status === "paused" ? "Paused" : rate > 0 ? formatRate(rate) : "—";
  metricModeLabel.textContent = isBackup ? "Compression" : "Verification";
  metricMode.textContent = isBackup ? "gzip" : job.phase === "readback" ? "Read-back" : "SHA-256";
  metricModeDetail.textContent = isBackup ? "Fast streaming mode" : "Image integrity protected";
  metricAccessLabel.textContent = isBackup ? "Source access" : "Target access";
  metricAccess.textContent = isBackup ? "Read only" : "Write device";
  metricAccessDetail.textContent = isBackup ? "Live consistency" : "Removable media";
  activityTime.textContent = formatClock(elapsedSeconds(job));
  activityPrimary.textContent = job.message || "Preparing the pipeline";
  activitySecondary.textContent = routeText(job);
  activityBytes.textContent = `${bytes.toLocaleString()} bytes ${job.phase === "readback" ? "verified" : "transferred"}`;
  progressFooterCopy.textContent = isBackup ? "Partial backup data is removed automatically if the stream is cancelled." : "Cancelling a restore may leave incomplete data on the target drive.";
  pauseButton.disabled = !active || job.status === "cancelling";
  pauseLabel.textContent = job.status === "paused" ? "Resume Job" : "Pause Job";
  cancelButton.disabled = job.status === "cancelling";
  cancelButton.className = active ? "progress-button cancel" : "progress-button";
  cancelLabel.textContent = active ? `Cancel ${isBackup ? "Backup" : "Restore"}` : "Close Details";
};

const openJobDetails = (id: string): void => {
  if (!jobs.has(id)) return;
  selectedJobID = id;
  cancelConfirm.classList.add("hidden");
  jobDetailsOverlay.hidden = false;
  jobDetailsOverlay.classList.remove("hidden");
  renderJobs();
  renderJobDetails();
  backgroundButton.focus();
};

const closeJobDetails = (): void => {
  cancelConfirm.classList.add("hidden");
  jobDetailsOverlay.classList.add("hidden");
  jobDetailsOverlay.hidden = true;
  const selectedCard = Array.from(jobsList.querySelectorAll<HTMLElement>(".job-card"))
    .find((card) => card.dataset.jobId === selectedJobID);
  selectedCard?.querySelector<HTMLButtonElement>(".job-details-button")?.focus();
};

const openRestoreConfirmation = (): void => {
  if (!selectedBackup || !deviceSelect.value) {
    showError(new Error("Choose a backup image and target drive first."));
    return;
  }
  const target = availableDevices.find((device) => device.path === deviceSelect.value);
  restoreConfirmImage.textContent = fileName(selectedBackup.path);
  restoreConfirmImage.title = selectedBackup.path;
  restoreConfirmTarget.textContent = target ? `${target.name} · ${target.path}` : deviceSelect.value;
  restoreConfirmTarget.title = deviceSelect.value;
  restoreConfirmOverlay.hidden = false;
  restoreConfirmOverlay.classList.remove("hidden");
  confirmRestoreButton.focus();
};

const closeRestoreConfirmation = (): void => {
  restoreConfirmOverlay.classList.add("hidden");
  restoreConfirmOverlay.hidden = true;
  restoreButton.focus();
};

Events.On("malina:job", (event) => { upsertJob(event.data); });

document.querySelectorAll<HTMLButtonElement>(".tab").forEach((tab) => {
  tab.addEventListener("click", () => switchView(tab.dataset.view ?? "backup-view"));
});

document.querySelectorAll<HTMLButtonElement>(".job-filter").forEach((button) => {
  button.addEventListener("click", () => {
    currentJobFilter = (button.dataset.jobFilter ?? "all") as JobFilter;
    document.querySelectorAll<HTMLButtonElement>(".job-filter").forEach((filter) => filter.classList.toggle("active", filter === button));
    renderJobs();
  });
});

jobSearch.addEventListener("input", renderJobs);

jobsList.addEventListener("click", (event) => {
  const target = event.target as Element;
  const card = target.closest<HTMLElement>(".job-card");
  const id = card?.dataset.jobId;
  if (!id) return;
  if (target.closest(".job-details-button")) {
    openJobDetails(id);
    return;
  }
  selectedJobID = id;
  renderJobs();
});

jobsList.addEventListener("keydown", (event) => {
  if (event.key !== "Enter" && event.key !== " ") return;
  const target = event.target as Element;
  if (target.closest("button")) return;
  const id = target.closest<HTMLElement>(".job-card")?.dataset.jobId;
  if (!id) return;
  event.preventDefault();
  openJobDetails(id);
});

document.querySelectorAll<HTMLInputElement>('input[name="auth-method"]').forEach((radio) => {
  radio.addEventListener("change", () => {
    element<HTMLElement>("key-auth").classList.toggle("hidden", authMethod() !== "key");
    element<HTMLElement>("password-auth").classList.toggle("hidden", authMethod() !== "password");
    resetConnectionStatus();
  });
});

[hostInput, portInput, passwordInput].forEach((input) => input.addEventListener("input", resetConnectionStatus));

togglePasswordButton.addEventListener("click", () => {
  const reveal = passwordInput.type === "password";
  passwordInput.type = reveal ? "text" : "password";
  togglePasswordButton.textContent = reveal ? "Hide" : "Show";
  togglePasswordButton.setAttribute("aria-pressed", String(reveal));
  passwordInput.focus();
});

element<HTMLButtonElement>("choose-key").addEventListener("click", () => {
  void runOperation(async () => {
    const path = await Service.SelectIdentityFile(identityPath);
    if (!path) return;
    identityPath = path;
    identityDisplay.textContent = path;
    identityDisplay.title = path;
    resetConnectionStatus();
  });
});

element<HTMLButtonElement>("choose-output").addEventListener("click", () => {
  void runOperation(async () => {
    const path = await Service.SelectBackupDirectory(outputDirectory);
    if (!path) return;
    outputDirectory = path;
    outputDisplay.textContent = path;
    outputDisplay.title = path;
  });
});

inspectButton.addEventListener("click", () => {
  if (busy || connectionCheck) return;
  let sourceConnection: Connection;
  try {
    sourceConnection = connection();
  } catch (error) {
    operation.classList.add("hidden");
    setConnectionError(error);
    return;
  }
  void (async () => {
    setBusy(true);
    operation.classList.add("hidden");
    setConnectionMessage("Checking connection…");
    setConnectionCheckActive(true);
    const request = Service.Inspect(sourceConnection);
    connectionCheck = request;
    try {
      setConnectionStatus(await request);
    } catch (error) {
      if (error instanceof CancelError || (error instanceof Error && error.name === "CancelError")) {
        setConnectionMessage("Connection check cancelled.");
      } else {
        setConnectionError(error);
      }
    } finally {
      if (connectionCheck === request) connectionCheck = null;
      setConnectionCheckActive(false);
      setBusy(false);
    }
  })();
});

cancelInspectButton.addEventListener("click", () => {
  if (!connectionCheck) return;
  cancelInspectButton.disabled = true;
  setConnectionMessage("Stopping connection check…");
  void connectionCheck.cancel("Connection check cancelled by the user.");
});

backupForm.addEventListener("submit", (event) => {
  event.preventDefault();
  void runOperation(async () => {
    if (!outputDirectory) throw new Error("Choose a folder for the backup.");
    const job = await Service.StartBackup({ connection: connection(), outputDirectory });
    passwordInput.value = "";
    upsertJob(job);
    switchView("jobs-view");
    openJobDetails(job.id);
  });
});

pauseButton.addEventListener("click", () => {
  const runtime = jobs.get(selectedJobID);
  if (!runtime || !isActiveJob(runtime.job) || runtime.job.status === "cancelling") return;
  void (async () => {
    pauseButton.disabled = true;
    try {
      const changed = runtime.job.status === "paused" ? await Service.ResumeJob(selectedJobID) : await Service.PauseJob(selectedJobID);
      if (!changed) throw new Error("The job state changed before the request could be applied.");
    } catch (error) {
      showError(error);
      renderJobDetails();
    }
  })();
});

backgroundButton.addEventListener("click", closeJobDetails);

cancelButton.addEventListener("click", () => {
  const runtime = jobs.get(selectedJobID);
  if (!runtime) return;
  if (!isActiveJob(runtime.job)) { closeJobDetails(); return; }
  const isBackup = runtime.job.type === "backup";
  cancelConfirmTitle.textContent = `Cancel this ${isBackup ? "backup" : "restore"}?`;
  cancelConfirmCopy.textContent = isBackup ? "The SSH stream will stop and the incomplete backup will be deleted." : "The restore will stop. The target drive may contain incomplete data and should not be used until it is restored again.";
  confirmCancelButton.textContent = `Cancel ${isBackup ? "Backup" : "Restore"}`;
  cancelConfirm.classList.remove("hidden");
  keepJobButton.focus();
});

keepJobButton.addEventListener("click", () => {
  cancelConfirm.classList.add("hidden");
  cancelButton.focus();
});

confirmCancelButton.addEventListener("click", () => {
  if (!selectedJobID) return;
  void (async () => {
    confirmCancelButton.disabled = true;
    try {
      if (!await Service.CancelJob(selectedJobID)) throw new Error("This job is no longer running.");
      cancelConfirm.classList.add("hidden");
    } catch (error) {
      showError(error);
    } finally {
      confirmCancelButton.disabled = false;
    }
  })();
});

jobDetailsOverlay.addEventListener("click", (event) => {
  if (event.target === jobDetailsOverlay) closeJobDetails();
});

restoreConfirmOverlay.addEventListener("click", (event) => {
  if (event.target === restoreConfirmOverlay) closeRestoreConfirmation();
});

document.addEventListener("keydown", (event) => {
  if (event.key !== "Escape") return;
  if (!helpOverlay.hidden) {
    closeHelp();
    return;
  }
  if (!restoreConfirmOverlay.hidden) {
    closeRestoreConfirmation();
    return;
  }
  if (jobDetailsOverlay.hidden) return;
  if (!cancelConfirm.classList.contains("hidden")) {
    cancelConfirm.classList.add("hidden");
    cancelButton.focus();
  } else closeJobDetails();
});

element<HTMLButtonElement>("choose-backup").addEventListener("click", () => {
  void runOperation(async () => {
    const backup = await Service.SelectBackup();
    if (!backup.path) return;
    selectedBackup = backup;
    renderSelectedBackup();
  });
});

element<HTMLButtonElement>("refresh-devices").addEventListener("click", () => { void runOperation(refreshDevices); });

deviceSelect.addEventListener("change", () => {
  updateTargetSummary();
  updateRestoreAvailability();
});

restoreForm.addEventListener("submit", (event) => {
  event.preventDefault();
  openRestoreConfirmation();
});

cancelRestoreConfirmButton.addEventListener("click", closeRestoreConfirmation);

confirmRestoreButton.addEventListener("click", () => {
  if (busy || !selectedBackup || !deviceSelect.value) return;
  const backupPath = selectedBackup.path;
  const targetDevice = deviceSelect.value;
  void (async () => {
    confirmRestoreButton.disabled = true;
    cancelRestoreConfirmButton.disabled = true;
    await runOperation(async () => {
      const job = await Service.StartRestore({
        backupPath,
        device: targetDevice,
        confirmation: targetDevice,
        verifyWrite: verifyWriteInput.checked,
      });
      closeRestoreConfirmation();
      upsertJob(job);
      switchView("jobs-view");
      openJobDetails(job.id);
    });
    confirmRestoreButton.disabled = false;
    cancelRestoreConfirmButton.disabled = false;
  })();
});

window.setInterval(() => {
  if (Array.from(jobs.values()).some(({ job }) => isActiveJob(job))) {
    renderJobs();
    if (!jobDetailsOverlay.hidden) renderJobDetails();
  }
}, 1000);

void (async () => {
  try {
    const [directory, existingJobs] = await Promise.all([Service.DefaultBackupDirectory(), Service.ListJobs()]);
    outputDirectory = directory;
    outputDisplay.textContent = outputDirectory;
    outputDisplay.title = outputDirectory;
    for (const job of existingJobs ?? []) upsertJob(job);
    renderJobs();
  } catch (error) {
    showError(error);
  }
})();
