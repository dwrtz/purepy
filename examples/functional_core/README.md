# Verified functional core

This ordinary Python 3.14 example exercises the [0.2 language](../../docs/FUNCTIONAL_CORE.md):
composition, folds, immutable chains, ASCII string operations, and persistent grouping.

```sh
bin/purepy check examples/functional_core
python3 -I -S -B examples/functional_core/check_runtime.py
```

The runtime checks compare results with independent Python oracles, exercise long
chains and persistence, and verify that no PurePy module is imported. The grouping
tree is illustrative and unbalanced; it is not a production collection.
