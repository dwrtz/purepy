"""Data-only nominal records, with no registry or runtime effect machinery.

The static verifier remains authoritative about source syntax. These checks catch
accidental misuse by ordinary Python callers; they are not a Python sandbox.
"""

from dataclasses import dataclass, fields
_CLASS_METADATA = frozenset({
    "__module__", "__qualname__", "__doc__", "__annotations__",
    "__dict__", "__weakref__", "__firstlineno__", "__static_attributes__",
    "__annotate__", "__annotate_func__", "__annotations_cache__",
})


def _reject_subclass(cls, **kwargs):
    raise TypeError("@value records cannot be subclassed")


def value(cls):
    """Create a frozen, slotted record from annotated fields without defaults.

    Positional and explicit keyword construction use declaration order. The
    verifier validates field types and rejects recursive record definitions. The
    host must supply exact Pure Values, including immutable external values whose
    contracts live in manifests. This runtime neither loads manifests nor eagerly
    resolves Python 3.14 deferred annotations.
    """
    if type(cls) is not type or cls.__bases__ != (object,):
        raise TypeError("@value requires a plain class without inheritance or a metaclass")
    for name in cls.__dict__:
        if name not in _CLASS_METADATA:
            raise TypeError(f"@value class bodies contain only annotated fields, not {name!r}")
    record = dataclass(frozen=True, slots=True)(cls)
    for field in fields(record):
        if field.name.startswith("__") and field.name.endswith("__"):
            raise TypeError("@value fields cannot use reserved double-underscore names")
    record.__init_subclass__ = classmethod(_reject_subclass)
    return record
