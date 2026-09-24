const navToggle = document.querySelector(".nav-toggle");
const nav = document.querySelector(".site-nav");

navToggle?.addEventListener("click", () => {
  const open = navToggle.getAttribute("aria-expanded") === "true";
  navToggle.setAttribute("aria-expanded", String(!open));
  nav?.classList.toggle("open", !open);
});

nav?.querySelectorAll("a").forEach((link) => link.addEventListener("click", () => {
  navToggle?.setAttribute("aria-expanded", "false");
  nav.classList.remove("open");
}));

document.querySelector("#year").textContent = String(new Date().getFullYear());

document.querySelectorAll("[data-screen]").forEach((tab) => {
  tab.addEventListener("click", () => {
    const screen = tab.dataset.screen;
    document.querySelectorAll("[data-screen]").forEach((candidate) => {
      const active = candidate === tab;
      candidate.classList.toggle("active", active);
      candidate.setAttribute("aria-selected", String(active));
    });
    document.querySelectorAll("[data-screen-image]").forEach((image) => image.classList.toggle("active", image.dataset.screenImage === screen));
    document.querySelectorAll("[data-screen-copy]").forEach((copy) => copy.classList.toggle("active", copy.dataset.screenCopy === screen));
  });
});

const copyCommand = async (text) => {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }

  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.append(textarea);
  textarea.select();
  const copied = document.execCommand("copy");
  textarea.remove();
  if (!copied) throw new Error("Clipboard access is unavailable");
};

document.querySelectorAll(".copy-command").forEach((button) => {
  button.addEventListener("click", async () => {
    const block = button.closest(".command-block");
    const status = block?.querySelector(".copy-status");
    const command = block?.querySelector("code")?.textContent?.trim();
    if (!command) return;

    button.disabled = true;
    try {
      await copyCommand(command);
      button.textContent = "Copied";
      button.classList.add("copied");
      status.textContent = "Command copied to clipboard.";
    } catch {
      button.textContent = "Try again";
      button.classList.add("failed");
      status.textContent = "Could not copy the command. Please select and copy it manually.";
    }

    window.setTimeout(() => {
      button.textContent = "Copy";
      button.classList.remove("copied", "failed");
      button.disabled = false;
    }, 1800);
  });
});

const matchers = {
  macos: /macos-universal\.zip$/i,
  windows: /windows-amd64\.zip$/i,
};
const linuxMatchers = {
  amd64: /linux-amd64\.tar\.gz$/i,
  arm64: /linux-arm64\.tar\.gz$/i,
};

const platformName = navigator.userAgentData?.platform || navigator.platform || navigator.userAgent;
const currentPlatform = /mac/i.test(platformName) ? "macos" : /win/i.test(platformName) ? "windows" : /linux|x11/i.test(platformName) ? "linux" : null;
if (currentPlatform) document.querySelector(`[data-platform-card="${currentPlatform}"]`)?.classList.add("recommended");

const linuxArchitecture = async () => {
  if (currentPlatform !== "linux") return null;
  try {
    const details = await navigator.userAgentData?.getHighEntropyValues?.(["architecture", "bitness"]);
    if (details?.architecture === "arm" && details.bitness === "64") return "arm64";
    if (details?.architecture === "x86" && details.bitness === "64") return "amd64";
  } catch {
    // Fall back to architecture hints in older browser user agents.
  }
  const userAgent = `${navigator.platform || ""} ${navigator.userAgent || ""}`;
  if (/\b(aarch64|arm64)\b/i.test(userAgent)) return "arm64";
  if (/\b(x86_64|amd64|x64)\b/i.test(userAgent)) return "amd64";
  return null;
};

fetch("https://api.github.com/repos/meltingcore/malina/releases?per_page=10", { headers: { Accept: "application/vnd.github+json" } })
  .then((response) => {
    if (!response.ok) throw new Error(String(response.status));
    return response.json();
  })
  .then(async (releases) => {
    const release = releases.find((candidate) => !candidate.draft && candidate.assets?.length);
    if (!release) return;
    const version = release.tag_name.startsWith("v") ? release.tag_name : `v${release.tag_name}`;
    document.querySelectorAll("[data-release-label]").forEach((label) => {
      label.innerHTML = `<i></i> Malina ${version} is available <b aria-hidden="true">→</b>`;
      label.href = release.html_url;
    });
    const arch = await linuxArchitecture();
    if (arch) matchers.linux = linuxMatchers[arch];
    const assets = {};
    for (const [platform, matcher] of Object.entries(matchers)) {
      assets[platform] = release.assets.find((asset) => matcher.test(asset.name));
      const link = document.querySelector(`[data-download="${platform}"]`);
      if (link && assets[platform]) {
        link.href = assets[platform].browser_download_url;
        link.setAttribute("download", "");
      }
    }
  })
  .catch(() => {});
