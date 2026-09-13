"""Static adapter dispatch. Shared cleanup policy and effects live elsewhere."""
from __future__ import annotations
import ctypes
import os
import re
import sys
try:
    from .runtime_adapters import BUILTINS
    from .runtime_adapters.claude import legacy_wrapper
except ImportError:
    from runtime_adapters import BUILTINS
    from runtime_adapters.claude import legacy_wrapper

def registry(modules):
    adapters, profiles = {}, {}
    for module in modules:
        name = module.ADAPTER_ID
        if not re.fullmatch(r"[a-z][a-z0-9_-]*", name) or name in adapters:
            raise ValueError("duplicate_or_invalid_adapter")
        if not callable(module.runtime) or not callable(module.prompt_reason):
            raise ValueError("invalid_adapter_callbacks")
        adapters[name] = module
        for key, profile in module.PROFILES.items():
            if key in profiles or profile.get("adapter") != name:
                raise ValueError("duplicate_or_mismatched_profile")
            if not all(isinstance(profile.get(field), str) and profile[field] for field in ("platform", "command", "version", "proof")):
                raise ValueError("incomplete_profile")
            if not isinstance(profile.get("capture_ansi"), bool) or not isinstance(profile.get("keys"), tuple) or not profile["keys"] or not all(isinstance(k, str) and k and not k.startswith("-") for k in profile["keys"]):
                raise ValueError("invalid_capture_or_exit_gesture")
            profiles[key] = dict(profile)
    return adapters, profiles

ADAPTERS, PROFILES = registry(BUILTINS)

def enrollment(profile, args, read_bytes):
    hook = getattr(ADAPTERS[profile["adapter"]], "enrollment", None)
    return hook(args, read_bytes) if hook else {}

def process_path(pid):
    """Read the OS executable path; no signals, audit tokens, or argv guesses."""
    if sys.platform == "darwin":
        lib = ctypes.CDLL("/usr/lib/libproc.dylib", use_errno=True)
        function = lib.proc_pidpath
        function.argtypes = [ctypes.c_int, ctypes.c_void_p, ctypes.c_uint32]
        function.restype = ctypes.c_int
        buffer = ctypes.create_string_buffer(4096)
        if function(int(pid), buffer, len(buffer)) <= 0:
            error = ctypes.get_errno()
            raise OSError(error, "proc_pidpath: " + os.strerror(error))
        return os.fsdecode(buffer.value)
    raise OSError("native executable observation is supported only on darwin")


def runtime(p, profile, record, *, process_path, legacy_wrapper):
    if sys.platform != profile["platform"]:
        return {}, "runtime_platform_unproven:" + sys.platform
    if not p.process_started or p.command != profile["command"]:
        return {}, "runtime_receiver_changed_or_unknown"
    adapter = ADAPTERS.get(profile.get("adapter"))
    if adapter is None:
        return {}, "unsupported_runtime_adapter"
    return adapter.runtime(p, profile, record, process_path=process_path, legacy_wrapper=legacy_wrapper)

def prompt_reason(h, profile, raw):
    adapter = ADAPTERS.get(profile.get("adapter"))
    return adapter.prompt_reason(h, profile, raw) if adapter else "unsupported_runtime_adapter"
