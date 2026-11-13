"""Inventory readers: one module per provider.

Each module registers its reader with `@reader("<provider>")` from `_base`. Every module in this
folder whose name does not start with `_` is imported when the package loads, so adding a reader
is adding one file here; no shared list has to change.
"""

import importlib
import pkgutil

from . import _base

READERS = _base.READERS


def load_all():
    """Import every reader module in this folder (sorted, so registration order is stable)."""
    for info in sorted(pkgutil.iter_modules(__path__), key=lambda item: item.name):
        if not info.name.startswith("_"):
            importlib.import_module(f"{__name__}.{info.name}")
    return READERS


load_all()
