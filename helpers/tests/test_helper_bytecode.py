"""Child commands must keep a source-only installation clean without shell env."""
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys


def test_generated_completion_commands_do_not_write_import_caches(tmp_path):
    source = Path(__file__).resolve().parents[1] / "tmux"
    copied = tmp_path / "installed-helpers"
    shutil.copytree(source, copied, ignore=shutil.ignore_patterns("__pycache__", "*.pyc"))
    env = {key: value for key, value in os.environ.items()
           if key not in {"PYTHONDONTWRITEBYTECODE", "PYTHONPYCACHEPREFIX", "PYTHONPATH"}}
    run_dir = tmp_path / "assignment"
    prepare = "import assignment,json;from pathlib import Path;print(json.dumps(assignment.prepare('claude',Path(" + repr(str(run_dir)) + "),['claude'])))"
    cp = subprocess.run([sys.executable, "-B", "-c", prepare], cwd=copied,
                        env=env, capture_output=True, text=True, check=True)
    prepared = json.loads(cp.stdout)
    hooks = json.loads((run_dir / "hooks.json").read_text())
    hook = hooks["hooks"]["Stop"][0]["hooks"][0]["command"]
    finish = re.search(r"Then run (.*?) --result", prepared["prompt_suffix"]).group(1)
    # Execute the generated command prefixes, with no inherited bytecode setting.
    # Help still imports the real sibling modules before argparse exits.
    for command in (hook, finish):
        subprocess.run([*shlex.split(command), "--help"], cwd=tmp_path, env=env,
                       capture_output=True, text=True, check=True)
    assert not list(copied.rglob("*.pyc"))
    assert not list(copied.rglob("__pycache__"))
