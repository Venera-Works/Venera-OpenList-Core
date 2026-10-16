#!/usr/bin/env python3
"""Exercise the desktop OpenList Core C ABI after a CI build."""

from __future__ import annotations

import ctypes
import json
import sys
import tempfile
from pathlib import Path


def invoke(library: ctypes.CDLL, request: dict[str, object]) -> dict[str, object]:
    pointer = library.OpenListInvoke(json.dumps(request).encode("utf-8"))
    if not pointer:
        raise RuntimeError("OpenListInvoke returned null")
    try:
        return json.loads(ctypes.string_at(pointer).decode("utf-8"))
    finally:
        library.OpenListFree(pointer)


def main() -> None:
    library_path = Path(sys.argv[1]).resolve()
    library = ctypes.CDLL(str(library_path))
    library.OpenListAllocate.argtypes = [ctypes.c_int]
    library.OpenListAllocate.restype = ctypes.c_void_p
    library.OpenListFree.argtypes = [ctypes.c_void_p]
    library.OpenListInvoke.argtypes = [ctypes.c_char_p]
    library.OpenListInvoke.restype = ctypes.c_void_p
    allocated = library.OpenListAllocate(8)
    if not allocated:
        raise RuntimeError("OpenListAllocate returned null")
    library.OpenListFree(allocated)
    with tempfile.TemporaryDirectory(prefix="venera-openlist-") as data_dir:
        initialized = invoke(library, {"method": "initialize", "arguments": {"dataDir": data_dir, "proxy": ""}})
        if initialized.get("error") or initialized.get("data", {}).get("drivers") != ["BaiduNetdisk", "Quark", "115 Open", "123 Open", "Onedrive", "GoogleDrive"]:
            raise RuntimeError(f"initialize failed: {initialized}")
        if invoke(library, {"method": "accounts", "arguments": {}}).get("error"):
            raise RuntimeError("accounts failed")
        if invoke(library, {"method": "close", "arguments": {}}).get("error"):
            raise RuntimeError("close failed")
        if invoke(library, {"method": "initialize", "arguments": {"dataDir": data_dir, "proxy": ""}}).get("error"):
            raise RuntimeError("reopen failed")
        invoke(library, {"method": "close", "arguments": {}})


if __name__ == "__main__":
    main()
