import type { Connection } from "../bindings/github.com/meltingcore/malina/internal/core/models.js";
import type { DeviceConfig, SavedDevice } from "../bindings/github.com/meltingcore/malina/internal/desktop/models.js";
import * as Service from "../bindings/github.com/meltingcore/malina/internal/desktop/service.js";
import { draftFingerprint, filterSavedDevices, normalizeDraft, samePasswordAccount, sshHost, suggestedDeviceName } from "./device-profiles.js";
import type { DeviceDraft } from "./device-profiles.js";

interface DeviceManagerHooks {
  readDraft(): DeviceDraft;
  applyDraft(draft: DeviceDraft): void;
  readPassword(): string;
  clearPassword(): void;
  setPassword(password: string): void;
  showBackup(): void;
  showDevices(): void;
  showError(error: unknown): void;
  isBusy(): boolean;
  defaultDirectory(): string;
  runOperation(task: () => Promise<void>): Promise<void>;
}

export const createDeviceManager = (hooks: DeviceManagerHooks) => {
  const get = <T extends HTMLElement>(id: string): T => document.getElementById(id) as T;
  const list = get<HTMLElement>("saved-devices-list");
  const empty = get<HTMLElement>("saved-devices-empty");
  const search = get<HTMLInputElement>("device-search");
  const selectedName = get<HTMLElement>("selected-profile-name");
  const selectedStatus = get<HTMLElement>("selected-profile-status");
  const saveButton = get<HTMLButtonElement>("save-device");
  const copyButton = get<HTMLButtonElement>("save-device-copy");
  const discardButton = get<HTMLButtonElement>("discard-device-changes");
  const saveStatus = get<HTMLElement>("device-save-status");
  const passwordNote = get<HTMLElement>("saved-password-note");
  const saveDialog = get<HTMLDialogElement>("save-device-dialog");
  const switchDialog = get<HTMLDialogElement>("switch-device-dialog");
  const deleteDialog = get<HTMLDialogElement>("delete-device-dialog");
  const nameInput = get<HTMLInputElement>("device-profile-name");
  const confirmSave = get<HTMLButtonElement>("confirm-save-device");
  const cancelSave = get<HTMLButtonElement>("cancel-save-device");
  const confirmDelete = get<HTMLButtonElement>("confirm-delete-device");
  const cancelDelete = get<HTMLButtonElement>("cancel-delete-device");
  const saveError = get<HTMLElement>("save-device-error");
  const deleteError = get<HTMLElement>("delete-device-error");
  type LoadedConfig = Omit<DeviceConfig, "devices"> & { devices: SavedDevice[] };
  const loaded = (value: DeviceConfig): LoadedConfig => ({ ...value, devices: value.devices || [] });
  let config: LoadedConfig = { version: 1, devices: [], selectedDeviceId: "" };
  let current: SavedDevice | null = null;
  let baseline = "";
  let savingCopy = false;
  let saving = false;
  let deleting: SavedDevice | null = null;
  let deletingInProgress = false;
  let pendingSwitch: (() => Promise<void>) | null = null;
  let afterSave: (() => Promise<void>) | null = null;
  const message = (error: unknown): string => error instanceof Error ? error.message : String(error);

  const isDirty = (): boolean => baseline !== draftFingerprint(hooks.readDraft()) ||
    (hooks.readDraft().rememberPassword && hooks.readPassword() !== "");

  const hasSavedPassword = (): boolean => !!current?.rememberPassword &&
    hooks.readDraft().rememberPassword && samePasswordAccount(current, hooks.readDraft());

  const changed = (): void => {
    const dirty = isDirty();
    selectedName.textContent = current?.name || "Unsaved device";
    selectedName.title = current ? `${current.name} · ${sshHost(hooks.readDraft())}` : "Configure and save a device";
    selectedStatus.textContent = current && dirty ? "Unsaved changes" : "Selected device";
    get<HTMLElement>("selected-profile").classList.toggle("dirty", !!current && dirty);
    get<HTMLElement>("selected-profile").setAttribute("aria-label", `${selectedStatus.textContent}: ${selectedName.textContent}. Browse saved devices.`);
    get("save-device-label").textContent = current ? "Save changes…" : "Save device…";
    copyButton.classList.toggle("hidden", !current);
    discardButton.classList.toggle("hidden", !current || !dirty);
    if (dirty) saveStatus.textContent = "";
    passwordNote.textContent = hasSavedPassword()
      ? "A password is saved for this account. Enter a password to replace it when saving."
      : "Stored in your system credential store when you save this device.";
    const password = get<HTMLInputElement>("password");
    password.placeholder = hasSavedPassword() ? "Saved password · enter to replace" : "Used for SSH and sudo";
  };

  const render = (): void => {
    list.replaceChildren();
    const sorted = filterSavedDevices(config.devices, search.value);
    empty.classList.toggle("hidden", sorted.length > 0);
    empty.querySelector("strong")!.textContent = config.devices.length ? "No matching devices" : "Your devices, ready next time";
    empty.querySelector("span:last-of-type")!.textContent = config.devices.length
      ? "Try a different search to find your device."
      : "Configure a connection and backup folder, then choose Save device.";
    get("add-first-device").classList.toggle("hidden", config.devices.length > 0);
    for (const device of sorted) {
      const card = document.createElement("article");
      card.className = `saved-device-card${current?.id === device.id ? " selected" : ""}`;
      const copy = document.createElement("div");
      copy.className = "saved-device-copy";
      const heading = document.createElement("h2");
      heading.textContent = device.name;
      if (current?.id === device.id) {
        const badge = document.createElement("span");
        badge.className = "device-selected-badge";
        badge.textContent = "Selected";
        heading.append(badge);
      }
      const address = document.createElement("p");
      const method = device.authMethod === "password" ? (device.rememberPassword ? "Saved password" : "Password when needed")
        : device.identity ? "SSH key" : "Default SSH key or agent";
      address.textContent = `${sshHost(device)}${device.port !== 22 ? ` · Port ${device.port}` : ""} · ${method}`;
      const folder = document.createElement("p");
      folder.textContent = device.outputDirectory;
      folder.title = device.outputDirectory;
      folder.className = "device-folder";
      copy.append(heading, address, folder);
      const actions = document.createElement("div");
      actions.className = "saved-device-actions";
      for (const [action, label] of [["use", "Use"], ["edit", "Edit"], ["delete", "Delete"]]) {
        const button = document.createElement("button");
        button.type = "button";
        button.className = `button compact action-button ${action === "use" ? "primary" : "secondary"}`;
        button.textContent = label;
        button.setAttribute("aria-label", `${label} ${device.name}`);
        button.dataset.deviceId = device.id;
        button.dataset.deviceAction = action;
        button.disabled = hooks.isBusy();
        actions.append(button);
      }
      card.append(copy, actions);
      list.append(card);
    }
    changed();
  };

  search.addEventListener("input", render);
  const apply = (device: SavedDevice | null): void => {
    current = device;
    hooks.applyDraft(device || {
      username: "", host: "", port: 22, authMethod: "key", identity: "",
      outputDirectory: hooks.defaultDirectory(), rememberPassword: false,
    });
    baseline = draftFingerprint(hooks.readDraft());
    saveStatus.textContent = "";
    render();
  };

  const openSave = (copy = false): void => {
    savingCopy = copy;
    nameInput.value = current && !copy ? current.name : suggestedDeviceName(hooks.readDraft().host, config.devices.map((device) => device.name));
    get<HTMLElement>("save-device-title").textContent = current && !copy ? "Save device changes" : "Save device";
    confirmSave.textContent = current && !copy ? "Save changes" : "Save device";
    saveError.textContent = "";
    saveDialog.showModal();
    nameInput.select();
  };

  const guardSwitch = (action: () => Promise<void>): void => {
    if (hooks.isBusy()) return;
    if (!isDirty()) {
      void hooks.runOperation(action);
      return;
    }
    pendingSwitch = action;
    switchDialog.showModal();
    get<HTMLButtonElement>("cancel-device-switch").focus();
  };

  const select = (device: SavedDevice, edit: boolean): void => {
    guardSwitch(async () => {
      config = loaded(await Service.SelectSavedDevice(device.id));
      apply(config.devices.find((saved) => saved.id === device.id) || null);
      hooks.showBackup();
      window.setTimeout(() => get<HTMLInputElement>(edit ? "username" : "host").focus(), 0);
    });
  };

  const add = (): void => guardSwitch(async () => {
    config = loaded(await Service.SelectSavedDevice(""));
    apply(null);
    hooks.showBackup();
    window.setTimeout(() => get<HTMLInputElement>("username").focus(), 0);
  });

  list.addEventListener("click", (event) => {
    if (hooks.isBusy()) return;
    const button = (event.target as Element).closest<HTMLButtonElement>("button[data-device-id]");
    const device = config.devices.find((saved) => saved.id === button?.dataset.deviceId);
    if (!device || !button) return;
    if (button.dataset.deviceAction === "delete") {
      const openDelete = async (): Promise<void> => {
        deleting = device;
        get<HTMLElement>("delete-device-copy").textContent = `Delete “${device.name}” and its remembered credentials? Existing backups will remain.`;
        deleteError.textContent = "";
        deleteDialog.showModal();
        cancelDelete.focus();
      };
      if (current?.id === device.id && isDirty()) guardSwitch(openDelete);
      else void openDelete();
    } else select(device, button.dataset.deviceAction === "edit");
  });

  get<HTMLButtonElement>("add-saved-device").addEventListener("click", add);
  get<HTMLButtonElement>("add-first-device").addEventListener("click", add);
  get<HTMLButtonElement>("selected-profile").addEventListener("click", hooks.showDevices);
  saveButton.addEventListener("click", () => { if (!hooks.isBusy()) openSave(); });
  copyButton.addEventListener("click", () => { if (!hooks.isBusy()) openSave(true); });
  discardButton.addEventListener("click", () => {
    if (!hooks.isBusy()) apply(current);
  });

  get<HTMLFormElement>("save-device-form").addEventListener("submit", (event) => {
    event.preventDefault();
    if (saving) return;
    void (async () => {
      saving = true;
      confirmSave.disabled = cancelSave.disabled = true;
      nameInput.disabled = true;
      try {
        const draft = normalizeDraft(hooks.readDraft());
        const enteredPassword = hooks.readPassword();
        const previous = current;
        config = loaded(await Service.SaveDevice({
          device: { ...draft, id: savingCopy ? "" : current?.id || "", name: nameInput.value },
          password: draft.rememberPassword ? hooks.readPassword() : undefined,
          copyPasswordFromId: savingCopy && current && hasSavedPassword() ? current.id : undefined,
        }));
        current = config.devices.find((device) => device.id === config.selectedDeviceId) || null;
        // Canonicalize fields without keeping a password in the DOM.
        if (current) hooks.applyDraft(current);
        // Keep an unremembered password available for the next operation in this
        // session; remembered passwords are retrieved only when needed.
        if (current?.authMethod === "password" && !current.rememberPassword) hooks.setPassword(enteredPassword);
        else hooks.clearPassword();
        baseline = draftFingerprint(hooks.readDraft());
        saveDialog.close();
        render();
        saveStatus.textContent = `Saved “${current?.name || previous?.name || "device"}”.`;
        const action = afterSave;
        afterSave = null;
        if (action) await hooks.runOperation(action);
      } catch (error) {
        saveError.textContent = message(error);
      } finally {
        saving = false;
        confirmSave.disabled = cancelSave.disabled = false;
        nameInput.disabled = false;
        if (saveDialog.open) nameInput.focus();
      }
    })();
  });

  const cancelSaving = (): void => {
    if (saving) return;
    afterSave = null;
    saveDialog.close();
  };
  cancelSave.addEventListener("click", cancelSaving);
  saveDialog.addEventListener("cancel", (event) => {
    if (saving) event.preventDefault();
    else afterSave = null;
  });

  get<HTMLButtonElement>("cancel-device-switch").addEventListener("click", () => {
    pendingSwitch = null;
    switchDialog.close();
  });
  switchDialog.addEventListener("cancel", () => { pendingSwitch = null; });
  get<HTMLButtonElement>("discard-device-switch").addEventListener("click", () => {
    const action = pendingSwitch;
    pendingSwitch = null;
    switchDialog.close();
    if (action) void hooks.runOperation(action);
  });
  get<HTMLButtonElement>("save-before-switch").addEventListener("click", () => {
    afterSave = pendingSwitch;
    pendingSwitch = null;
    switchDialog.close();
    openSave();
  });

  confirmDelete.addEventListener("click", () => {
    if (!deleting || deletingInProgress) return;
    const device = deleting;
    void (async () => {
      deletingInProgress = true;
      confirmDelete.disabled = cancelDelete.disabled = true;
      try {
        config = loaded(await Service.DeleteSavedDevice(device.id));
        if (current?.id === device.id) apply(null);
        else render();
        deleteDialog.close();
        get<HTMLButtonElement>("add-saved-device").focus();
      } catch (error) {
        deleteError.textContent = message(error);
      } finally {
        deletingInProgress = false;
        confirmDelete.disabled = cancelDelete.disabled = false;
      }
    })();
  });
  cancelDelete.addEventListener("click", () => { deleting = null; deleteDialog.close(); });
  deleteDialog.addEventListener("cancel", (event) => {
    if (deletingInProgress) event.preventDefault();
    else deleting = null;
  });

  return {
    changed,
    initialize: async (): Promise<void> => {
      baseline = draftFingerprint(hooks.readDraft());
      try {
        config = loaded(await Service.LoadDeviceConfig());
        apply(config.devices.find((device) => device.id === config.selectedDeviceId) || null);
      } catch (error) {
        empty.querySelector("strong")!.textContent = "Saved devices could not be loaded";
        empty.querySelector("span:last-of-type")!.textContent = "Your configuration file has been preserved. See the error for recovery instructions.";
        hooks.showError(error);
        changed();
      }
    },
    passwordFor: async (connection: Connection): Promise<string> => {
      if (hooks.readPassword()) return hooks.readPassword();
      if (hasSavedPassword() && current) {
        try {
          return await Service.SavedDevicePassword(current.id, connection);
        } catch (error) {
          window.setTimeout(() => get<HTMLInputElement>("password").focus(), 0);
          throw error;
        }
      }
      window.setTimeout(() => get<HTMLInputElement>("password").focus(), 0);
      throw new Error("Enter the SSH password for this account.");
    },
    jobProfileName: (): string => current ? `${current.name}${isDirty() ? " (unsaved changes)" : ""}` : "",
  };
};
