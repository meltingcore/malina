import type { SavedDevice } from "../bindings/github.com/meltingcore/malina/internal/desktop/models.js";

export type DeviceDraft = Pick<SavedDevice, "username" | "host" | "port" | "authMethod" | "identity" | "outputDirectory" | "rememberPassword">;

export const filterSavedDevices = (devices: SavedDevice[], search: string): SavedDevice[] => {
  const query = search.trim().toLowerCase();
  return devices.filter((device) =>
    [device.name, sshHost(device), String(device.port), device.outputDirectory].some((value) => value.toLowerCase().includes(query)))
    .sort((a, b) => a.name.localeCompare(b.name));
};

export const normalizeDraft = (draft: DeviceDraft): DeviceDraft => ({
  username: draft.username.trim(),
  host: draft.host.trim().replace(/^\[|\]$/g, ""),
  port: draft.port,
  authMethod: draft.authMethod,
  identity: draft.authMethod === "key" ? draft.identity?.trim() || "" : "",
  outputDirectory: draft.outputDirectory.trim(),
  rememberPassword: draft.authMethod === "password" && draft.rememberPassword,
});

export const draftFingerprint = (draft: DeviceDraft): string => {
  const value = normalizeDraft(draft);
  return JSON.stringify([value.username, value.host, value.port, value.authMethod, value.identity,
    value.outputDirectory, value.rememberPassword]);
};

export const samePasswordAccount = (a: DeviceDraft, b: DeviceDraft): boolean => {
  const first = normalizeDraft(a);
  const second = normalizeDraft(b);
  return first.authMethod === "password" && second.authMethod === "password" &&
    first.username === second.username && first.host.toLowerCase() === second.host.toLowerCase() && first.port === second.port;
};

export const sshHost = (draft: DeviceDraft): string => {
  const value = normalizeDraft(draft);
  const host = value.host.includes(":") ? `[${value.host}]` : value.host;
  return `${value.username}@${host}`;
};

export const suggestedDeviceName = (host: string, names: string[]): string => {
  const base = (host.trim().split("@").pop()?.replace(/^\[|\]$/g, "") || "Raspberry Pi").slice(0, 90);
  const existing = new Set(names.map((name) => name.toLowerCase()));
  let name = base;
  for (let suffix = 2; existing.has(name.toLowerCase()); suffix++) name = `${base} (${suffix})`;
  return name.slice(0, 100);
};
