"""One interpretation of review retention; expiry never proves completion."""
from datetime import datetime, timezone


def review_active(meta, now=None):
    if not meta.get("hold_reason", "").strip():
        return False
    try:
        deadline = datetime.fromisoformat(meta.get("hold_until", "").replace("Z", "+00:00"))
        if deadline.tzinfo is None:
            return True
    except (ValueError, TypeError):
        return True  # Legacy and malformed holds remain protected.
    return (now or datetime.now(timezone.utc)) < deadline


def active(meta, now=None):
    return meta.get("keep_open") == "1" or review_active(meta, now)
