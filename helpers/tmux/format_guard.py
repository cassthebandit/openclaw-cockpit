"""Exact tmux format predicates, including ordinary punctuation in metadata.

Tmux FORMATS documents ##, #, and #} as literal #, comma and closing brace.
Escape hash first so user text cannot introduce variables, jobs or operators.
Values must be expanded only once: do not wrap these predicates in E: or T:.
"""
from __future__ import annotations

import re


def literal(value: object) -> str:
    text = str(value)
    if any(ord(char) < 32 or ord(char) == 127 for char in text):
        raise ValueError("unsafe_guard_control")
    return text.replace("#", "##").replace(",", "#,").replace("}", "#}")


def all_equal(expected: dict[str, object]) -> str:
    """Compare every named variable/option with its exact literal snapshot."""
    if not expected:
        raise ValueError("empty_guard")
    predicates = []
    for name, value in expected.items():
        if not re.fullmatch(r"@?[A-Za-z_][A-Za-z0-9_-]*", name):
            raise ValueError("unsafe_guard_variable")
        predicates.append("#{==:#{" + name + "}," + literal(value) + "}")
    condition = predicates[0]
    for predicate in predicates[1:]:
        condition = "#{&&:" + condition + "," + predicate + "}"
    return condition
