#!/usr/bin/env python3
"""Install a script-backed app; optionally pin it to the Dock. No dependencies."""
import argparse
import pathlib
import plistlib
import shutil
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--dock", action="store_true")
args = parser.parse_args()
root = pathlib.Path(__file__).resolve().parent.parent
app = pathlib.Path.home() / "Applications/OpenClaw Cockpit.app"
contents = app / "Contents"
resources = contents / "Resources"
macos = contents / "MacOS"
resources.mkdir(parents=True, exist_ok=True)
macos.mkdir(exist_ok=True)
shutil.copy2(root / "assets/cockpit.icns", resources / "cockpit.icns")
shutil.copy2(root / "scripts/cockpit.command", resources / "cockpit.command")
(resources / "cockpit.command").chmod(0o755)
with (contents / "Info.plist").open("wb") as stream:
    plistlib.dump({"CFBundleName": "OpenClaw Cockpit", "CFBundleIdentifier": "io.openclaw.cockpit.launcher",
                  "CFBundleExecutable": "launch", "CFBundleIconFile": "cockpit", "CFBundlePackageType": "APPL",
                  "CFBundleVersion": "1", "LSUIElement": True}, stream)
launcher = macos / "launch"
launcher.write_text('''#!/bin/bash
set -euo pipefail
resource_dir="$(cd "$(dirname "$0")/../Resources" && pwd)"
export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
"$resource_dir/cockpit.command" --ensure
client_ttys="$(tmux list-clients -t '=cass-agents' -F '#{client_tty}')"
exec /usr/bin/osascript - "$resource_dir/cockpit.command" "$client_ttys" <<'APPLESCRIPT'
on run argv
  tell application "Terminal"
    activate
    repeat with w in windows
      repeat with t in tabs of w
        if (paragraphs of item 2 of argv) contains (tty of t) then
          set selected of t to true
          set index of w to 1
          return
        end if
      end repeat
    end repeat
    set cockpitTab to do script quoted form of item 1 of argv
    set custom title of cockpitTab to "OpenClaw Cockpit"
  end tell
end run
APPLESCRIPT
''')
launcher.chmod(0o755)
if args.dock:
    raw = subprocess.check_output(["defaults", "export", "com.apple.dock", "-"])
    dock = plistlib.loads(raw)
    url = app.as_uri() + "/"
    if not any(item.get("tile-data", {}).get("file-data", {}).get("_CFURLString") == url
               for item in dock.get("persistent-apps", [])):
        backup = pathlib.Path.home() / "Library/Application Support/OpenClaw Cockpit"
        backup.mkdir(parents=True, exist_ok=True)
        (backup / "dock-before.plist").write_bytes(raw)
        tile = {"tile-data": {"file-data": {"_CFURLString": url, "_CFURLStringType": 15}}, "tile-type": "file-tile"}
        subprocess.run(["defaults", "write", "com.apple.dock", "persistent-apps", "-array-add",
                        plistlib.dumps(tile).decode()], check=True)
        subprocess.run(["killall", "Dock"], check=True)
print(app)
