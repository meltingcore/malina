import { describe, expect, it } from "vitest";
import { draftFingerprint, filterSavedDevices, normalizeDraft, samePasswordAccount, sshHost, suggestedDeviceName } from "./device-profiles.js";
import type { SavedDevice } from "../bindings/github.com/meltingcore/malina/internal/desktop/models.js";
import type { DeviceDraft } from "./device-profiles.js";

const draft: DeviceDraft = {
  username: "pi", host: "raspberrypi.local", port: 22, authMethod: "password", identity: "",
  outputDirectory: "/backups", rememberPassword: true,
};

describe("device profile state", () => {
  it("searches devices and folders case-insensitively", () => {
    const devices: SavedDevice[] = [
      { ...draft, id: "office", name: "Office Pi", outputDirectory: "/Backups/Office" },
      { ...draft, id: "workshop", name: "Workshop Pi", authMethod: "key", host: "workshop.local", port: 2222 },
    ];
    expect(filterSavedDevices(devices, " PI ")).toHaveLength(2);
    expect(filterSavedDevices(devices, "/backups/office").map((device) => device.id)).toEqual(["office"]);
    expect(filterSavedDevices(devices, "pi@workshop.local").map((device) => device.id)).toEqual(["workshop"]);
    expect(filterSavedDevices(devices, "2222").map((device) => device.id)).toEqual(["workshop"]);
    expect(filterSavedDevices(devices, "missing")).toEqual([]);
    expect(filterSavedDevices([...devices].reverse(), "").map((device) => device.id)).toEqual(["office", "workshop"]);
  });
  it("detects persistent edits without depending on a password field", () => {
    expect(draftFingerprint(draft)).toBe(draftFingerprint({ ...draft, host: " raspberrypi.local " }));
    for (const changed of [
      { ...draft, username: "other" }, { ...draft, host: "other.local" }, { ...draft, port: 2222 },
      { ...draft, outputDirectory: "/other-backups" }, { ...draft, rememberPassword: false },
    ]) expect(draftFingerprint(changed)).not.toBe(draftFingerprint(draft));
  });

  it("only reuses a saved password for the same SSH account", () => {
    expect(samePasswordAccount(draft, { ...draft, host: "RASPBERRYPI.local", outputDirectory: "/other" })).toBe(true);
    for (const changed of [
      { ...draft, username: "other" }, { ...draft, host: "other.local" },
      { ...draft, port: 2222 }, { ...draft, authMethod: "key" },
    ]) expect(samePasswordAccount(draft, changed)).toBe(false);
  });

  it("drops inactive authentication fields", () => {
    expect(normalizeDraft({ ...draft, identity: "/key" }).identity).toBe("");
    expect(normalizeDraft({ ...draft, authMethod: "key" }).rememberPassword).toBe(false);
  });

  it("handles bracketed and unbracketed IPv6 hosts", () => {
    expect(sshHost({ ...draft, host: "[2001:db8::1]" })).toBe("pi@[2001:db8::1]");
    expect(sshHost({ ...draft, host: "2001:db8::1" })).toBe("pi@[2001:db8::1]");
  });

  it("suggests host-based names without case-insensitive collisions", () => {
    expect(suggestedDeviceName("pi@raspberrypi.local", [])).toBe("raspberrypi.local");
    expect(suggestedDeviceName("raspberrypi.local", ["RASPBERRYPI.local", "raspberrypi.local (2)"])).toBe("raspberrypi.local (3)");
    expect(suggestedDeviceName("", [])).toBe("Raspberry Pi");
    const longName = "a".repeat(120);
    const first = suggestedDeviceName(longName, []);
    expect(suggestedDeviceName(longName, [first])).not.toBe(first);
    expect(suggestedDeviceName(longName, [first]).length).toBeLessThanOrEqual(100);
  });
});
