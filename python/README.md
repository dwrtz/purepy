# PurePy runtime

The package supplies the single runtime decorator admitted by PurePy:

```python
from purepy import value

@value
class User:
    user_id: int
    name: str

user = User(1, name="Ada")
```

Records are frozen, slotted, nominal, and data-only. The static verifier validates
field types and deep immutability, including homogeneous tuples, optional values,
project records, and manifest-declared external values. The runtime permits Python
3.14 deferred annotations without resolving forward records at decoration time.
There are no defaults, user methods, or inheritance. The host must supply values
of the exact declared types; the decorator does not repeat static type checking.

Use uv to create the repository's Python 3.14 virtual environment and install the
runtime. From the repository root, run:

```sh
make setup
make python-test
```

Python 3.12 and later execute this support package; PurePy source syntax targets
3.14. The managed development interpreter is `.venv/bin/python`. This runtime
performs no I/O and has no registry, verifier integration,
effect interpreter, or application framework. The verifier is authoritative about
class shapes, recursive records, and field types; arbitrary unverified callers
remain responsible for the contracts they pass into verified code.
