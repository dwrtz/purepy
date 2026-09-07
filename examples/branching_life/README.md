# Branching Life

A deterministic Conway's Life simulation on a 40 × 28 torus. At generation 20,
an alternate timeline flips one cell. Scrub through 120 generations to see the
futures diverge. Frames are computed by Python, not reimplemented in JavaScript.

The verified core demonstrates PurePy 0.2 generic recursive NamedTuple history,
a generic higher-order evolution function, a rule closure capturing immutable
data, and `copy.replace`. Forks share the unchanged history prefix; old worlds
remain available without undo operations. There are no manifests or host calls.

```sh
purepy check examples/branching_life --no-cache
purepy capabilities life.fork --config examples/branching_life/purepy.toml
python3 -I -S -B examples/branching_life/build_demo.py
```

Requires Python 3.14. `build_demo.py` is the unverified display host, outside
`src/`: it checks a still life, oscillator, repeatability, fork isolation and
shared ancestry, then embeds computed frames into `demo.html`, a Codex inline
visualization fragment using host theme variables. Supply an output path to
the build script to choose its destination. The UI supports
play/pause and timeline scrubbing; the alternate future is precomputed.

This small demo uses tuple concatenation to construct grids, which is quadratic
in cell count. It is not a large-grid performance benchmark. Core callers must
provide positive dimensions, binary cells of matching length, a valid fork
index and nonnegative step counts. Verification establishes language acceptance,
not these preconditions, termination, or absence of runtime errors.
