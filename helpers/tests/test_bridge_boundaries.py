"""Real SQLite snapshot isolation and portable producer contracts."""
import ast
import contextlib
import io
import json
import sqlite3
from pathlib import Path
from unittest.mock import patch

import pytest
from helpers.openclaw_runtime import cockpit_snapshot as snapshot, source
from test_bridge import build_state_db, write_rows


def test_concurrent_writer_cannot_mix_task_and_flow_generations(tmp_path):
    db = tmp_path / "state.sqlite"
    build_state_db(db)
    write_rows(db, tasks=({"task_id": "task", "task": "before", "parent_flow_id": "flow"},),
               flows=({"flow_id": "flow", "goal": "before"},))
    writer = sqlite3.connect(db)
    writer.execute("PRAGMA journal_mode=WAL").fetchall()
    writer.execute("UPDATE task_runs SET task=task")
    writer.commit()
    real = source.open_state_db(db, timeout=1)
    trace = []
    real.set_trace_callback(trace.append)

    class Cursor:
        def __init__(self, cursor):
            self.cursor = cursor
        def fetchall(self):
            rows = self.cursor.fetchall()
            # The first SELECT has drained before this independent writer commits.
            writer.execute("UPDATE task_runs SET task='after'")
            writer.execute("UPDATE flow_runs SET goal='after'")
            writer.commit()
            return rows

    class Connection:
        def __init__(self):
            self.closed = False
        @property
        def row_factory(self):
            return real.row_factory
        @row_factory.setter
        def row_factory(self, value):
            real.row_factory = value
        def execute(self, sql):
            cursor = real.execute(sql)
            return Cursor(cursor) if "FROM task_runs ORDER" in sql else cursor
        def close(self):
            self.closed = True
            real.close()

    con = Connection()
    try:
        with patch.object(source, "open_state_db", return_value=con):
            result = source.load_sqlite_payloads(db, timeout=1, now=1000)
        assert result.tasks["tasks"][0]["task"] == "before"
        assert result.flows["flows"][0]["goal"] == "before"
        assert writer.execute("SELECT goal FROM flow_runs").fetchone()[0] == "after"
        assert trace[0] == "BEGIN"
        assert con.closed
    finally:
        writer.close()
        if not con.closed:
            con.close()


def test_producer_error_is_bounded_on_both_channels():
    error = "producer broken\n" + "x" * 9000
    with patch.object(snapshot, "load_sources", side_effect=source.SnapshotError(error)), \
         contextlib.redirect_stdout(io.StringIO()) as out, contextlib.redirect_stderr(io.StringIO()) as err:
        assert snapshot.main([]) == 1
    payload = json.loads(out.getvalue())
    assert payload["ok"] is False
    assert len(payload["error"]) == 2048
    assert err.getvalue().strip() == payload["error"]


def test_delivery_count_distinguishes_prelimit_population():
    payloads = source.SourcePayloads(
        tasks={"tasks": [{"taskId": str(i), "status": "running", "task": "unique " + str(i)} for i in range(3)]},
        flows={"flows": []}, audit={"findings": []})
    result = snapshot.build_snapshot(payloads, limit=1)
    assert len(result["cards"]) == 1
    assert result["summary"]["visibleRuntimeCardCount"] == 1
    assert result["summary"]["totalVisibleRuntimeCardCount"] == 3
    assert result["truncated"] is True
    assert snapshot.build_snapshot(payloads, limit=0)["cards"] == []


def test_all_bridge_modules_have_no_process_execution():
    for path in Path(snapshot.__file__).parent.glob("*.py"):
        tree = ast.parse(path.read_text())
        for node in ast.walk(tree):
            if isinstance(node, (ast.Import, ast.ImportFrom)):
                modules = [node.module or ""] if isinstance(node, ast.ImportFrom) else [name.name for name in node.names]
                assert not any(name.split(".")[0] == "subprocess" for name in modules), path
            if isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute):
                assert node.func.attr not in {"system", "popen", "spawn", "execv", "execve", "posix_spawn"}, path
