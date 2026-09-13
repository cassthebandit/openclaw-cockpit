from datetime import datetime, timezone
import holds

NOW = datetime(2026, 9, 13, tzinfo=timezone.utc)


def test_review_expiry_is_shared_and_indefinite_is_explicit():
    assert not holds.active({"hold_reason": "review", "hold_until": "2026-09-12T00:00:00Z"}, NOW)
    assert holds.active({"hold_reason": "review", "hold_until": "2026-09-14T00:00:00Z"}, NOW)
    for deadline in ("", "bad", "2026-09-12"):
        assert holds.active({"hold_reason": "review", "hold_until": deadline}, NOW)
    assert holds.active({"keep_open": "1"}, NOW)
    assert not holds.active({"keep_open": "0"}, NOW)
