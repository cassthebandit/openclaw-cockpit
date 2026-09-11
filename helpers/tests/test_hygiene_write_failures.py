import argparse
import contextlib
import io
import json
import subprocess
from unittest.mock import patch

import pytest
from helpers.tmux import session_hygiene as hygiene
from test_hygiene import managed


@pytest.mark.parametrize("action", ["mark", "cancel_mark"])
@pytest.mark.parametrize("failed_write", [1, 2, 3])
def test_partial_metadata_failures_are_not_reported_as_success(action, failed_write):
    pane = managed("example")
    item = hygiene.result(session="example", action=action, reason="test", panes=[pane])
    writes = 0
    def run(*args, **kwargs):
        nonlocal writes
        writes += 1
        if writes == failed_write:
            raise subprocess.CalledProcessError(1, args, stderr="pane replaced")
        return subprocess.CompletedProcess(args, 0, "", "")
    args = argparse.Namespace(max_kills=1, json=True, status_file="", policy="kill-safe", grace=0, allow_session=[], override_hold=False)
    with patch.object(hygiene, "build_plan", return_value=[item]), \
         patch.object(hygiene, "revalidate_target", return_value=([pane], "ok")), \
         patch.object(hygiene, "run_tmux", side_effect=run), contextlib.redirect_stdout(io.StringIO()) as out:
        hygiene.cmd_apply(args)
    result = json.loads(out.getvalue())
    assert result["cancelled"] == result["marked"] == []
    assert result["refused"][0]["reason"] == "metadata_write_failed:pane replaced"
    assert writes == failed_write
