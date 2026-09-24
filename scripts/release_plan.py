#!/usr/bin/env python3
"""Select release builds from the assets already attached to a GitHub Release."""

import json
import os
import sys
from pathlib import Path


def plan(version: str, release: dict) -> dict[str, str]:
    assets = {asset["name"] for asset in release["assets"]}
    linux = [
        {"arch": arch, "runner": runner}
        for arch, runner in (("amd64", "ubuntu-24.04"), ("arm64", "ubuntu-24.04-arm"))
        if f"malina-{version}-linux-{arch}.tar.gz" not in assets
    ]
    macos = f"malina-{version}-macos-universal.zip" not in assets
    windows = f"malina-{version}-windows-amd64.zip" not in assets
    return {
        "linux_matrix": json.dumps({"include": linux}, separators=(",", ":")),
        "build_linux": str(bool(linux)).lower(),
        "build_macos": str(macos).lower(),
        "build_windows": str(windows).lower(),
        "build_any": str(bool(linux) or macos or windows).lower(),
    }


def main() -> None:
    version, release_path = sys.argv[1:3]
    result = plan(version, json.loads(Path(release_path).read_text()))
    with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
        for key, value in result.items():
            print(f"{key}={value}", file=output)
    for key, value in result.items():
        print(f"{key}={value}")


if __name__ == "__main__":
    main()
