import argparse
import unittest
from datetime import datetime, timedelta, timezone
from unittest.mock import patch
from helpers.tmux import session_hygiene as hygiene
from helpers.tmux import tmux_inspector as inspector
from test_hygiene import managed
from test_inspector import pane

class RetentionTests(unittest.TestCase):
    def test_expiry_removes_hold_but_never_proves_completion(self):
        now = datetime.now(timezone.utc)
        p = managed("test", hold_reason="review", state="running")
        self.assertTrue(hygiene.hold_is_active(p, now))
        p.meta["hold_until"] = (now-timedelta(hours=1)).isoformat()
        with patch.object(hygiene, "capture_pane_text", return_value="still working"):
            self.assertFalse(hygiene.hold_is_active(p, now))
            item = hygiene.eligible_managed(p.session, [p], policy="kill-safe", grace=0, now=now)
            self.assertNotEqual(item["action"], "kill")
            p.meta.update(state="done", completed_at=(now-timedelta(minutes=10)).isoformat())
            self.assertFalse(hygiene.hold_is_active(p, now))
            p.command = "claude"
            self.assertFalse(hygiene.hold_is_active(p, now))
            p.dead = True
            self.assertFalse(hygiene.hold_is_active(p, now))
            p.meta["hold_until"] = (now+timedelta(hours=24)).isoformat()
            self.assertTrue(hygiene.hold_is_active(p, now))

    def test_expired_hold_cleanup_and_renewal_at_apply(self):
        now = datetime.now(timezone.utc)
        p = managed("oc-vis-smoke-lease", hold_reason="review", hold_until=(now-timedelta(hours=1)).isoformat())
        p.dead = True
        item = hygiene.eligible_managed(p.session, [p], policy="kill-safe", grace=300, now=now)
        self.assertEqual(item["action"], "kill")
        p.meta["hold_until"] = (now+timedelta(hours=1)).isoformat()
        with patch.object(hygiene, "list_panes", return_value=[p]):
            _, status = hygiene.revalidate_target(item, allow_hold=False)
        self.assertEqual(status, "hold_reason_active_at_apply")

    def test_model_folder_does_not_make_server_an_agent(self):
        p = pane("fable-preview", command="Python", start_command="python3 -m http.server 5198", path="/tmp/claude-codex-fable")
        kind, agent, _, _ = inspector.infer_kind_agent_goal(p)
        self.assertEqual((kind, agent), ("detected-viewer", ""))
        p.command = "claude"
        self.assertEqual(inspector.infer_kind_agent_goal(p)[:2], ("detected-agent", "claude"))
