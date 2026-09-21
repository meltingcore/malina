#!/usr/bin/env bash

# Verify that every user-visible and platform-specific package version matches
# the newest release heading in CHANGELOG.md. Run this from the repository root.

set -euo pipefail

metadata_failed=0

# Record every mismatch instead of exiting at the first one so a version bump
# produces one useful CI report containing all files that still need updating.
require_version() {
  local file="$1"
  local field="$2"
  local actual="$3"
  local expected="$4"

  if [[ "${actual}" != "${expected}" ]]; then
    echo "::error file=${file}::${field} is '${actual:-<missing>}'; expected '${expected}' from the latest CHANGELOG.md entry."
    metadata_failed=1
  fi
}

# Read the string immediately following a named key in an Apple plist. Both
# release and development bundles are generated from build/config.yml.
plist_value() {
  local file="$1"
  local key="$2"

  awk -v key="${key}" '
    $0 ~ "<key>" key "</key>" {
      getline
      sub(/^[[:space:]]*<string>/, "")
      sub(/<\/string>[[:space:]]*$/, "")
      print
      exit
    }
  ' "${file}"
}

# The first changelog release heading is the single source of truth. Requiring
# a stable semantic version also prevents an accidental "Unreleased" build from
# being packaged as a release.
version="$(sed -n 's/^## \[\([^]]*\)\].*/\1/p' CHANGELOG.md | head -n 1)"
if [[ ! "${version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "::error file=CHANGELOG.md::Expected the latest changelog entry to contain a stable semantic version; found '${version:-<missing>}'."
  exit 1
fi

# Application and frontend metadata.
require_version build/config.yml "info.version" \
  "$(sed -n 's/^  version: "\([^"]*\)"/\1/p' build/config.yml)" "${version}"
require_version frontend/package.json "version" \
  "$(sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' frontend/package.json)" "${version}"
require_version frontend/index.html "version badge" \
  "$(sed -n 's/.*class="version-badge">v\([^<]*\)<.*/\1/p' frontend/index.html)" "${version}"

# Linux package metadata.
require_version build/linux/nfpm/nfpm.yaml "version" \
  "$(sed -n 's/^version:[[:space:]]*"\([^"]*\)".*/\1/p' build/linux/nfpm/nfpm.yaml)" "${version}"

# macOS uses the same semantic version for its display version and bundle build
# version in both production and development app bundles.
for plist in build/darwin/Info.plist build/darwin/Info.dev.plist; do
  require_version "${plist}" "CFBundleShortVersionString" \
    "$(plist_value "${plist}" CFBundleShortVersionString)" "${version}"
  require_version "${plist}" "CFBundleVersion" \
    "$(plist_value "${plist}" CFBundleVersion)" "${version}"
done

# Windows executable and installer metadata. MSIX requires four numeric version
# components, so its representation appends a zero to the semantic version.
require_version build/windows/info.json "fixed.file_version" \
  "$(sed -n 's/^[[:space:]]*"file_version":[[:space:]]*"\([^"]*\)".*/\1/p' build/windows/info.json)" "${version}"
require_version build/windows/info.json "ProductVersion" \
  "$(sed -n 's/^[[:space:]]*"ProductVersion":[[:space:]]*"\([^"]*\)".*/\1/p' build/windows/info.json)" "${version}"
require_version build/windows/wails.exe.manifest "assemblyIdentity version" \
  "$(sed -n 's/.*<assemblyIdentity[^>]*version="\([^"]*\)".*/\1/p' build/windows/wails.exe.manifest | head -n 1)" "${version}"
require_version build/windows/msix/app_manifest.xml "Identity Version" \
  "$(sed -n 's/.*Version="\([^"]*\)".*/\1/p' build/windows/msix/app_manifest.xml | head -n 1)" "${version}.0"
require_version build/windows/msix/template.xml "PackageInformation Version" \
  "$(sed -n 's/.*Version="\([^"]*\)".*/\1/p' build/windows/msix/template.xml | head -n 1)" "${version}.0"
require_version build/windows/nsis/wails_tools.nsh "INFO_PRODUCTVERSION" \
  "$(sed -n 's/^[[:space:]]*!define INFO_PRODUCTVERSION "\([^"]*\)".*/\1/p' build/windows/nsis/wails_tools.nsh)" "${version}"

exit "${metadata_failed}"
