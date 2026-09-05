# Pinned Unicode identifier and normalization inputs

These gzip files contain the exact Unicode 16.0.0 Character Database bytes.
Generation and conformance checks verify the uncompressed SHA-256 digests:

| Input | SHA-256 |
| --- | --- |
| [DerivedCoreProperties.txt](https://www.unicode.org/Public/16.0.0/ucd/DerivedCoreProperties.txt) | `39d35161f2954497f69e08bdb9e701493f476a3d30222de20028feda36c1dabd` |
| [DerivedNormalizationProps.txt](https://www.unicode.org/Public/16.0.0/ucd/DerivedNormalizationProps.txt) | `4d4c03892dea9146d674b686e495df2d55a28d071ac474041d73518f887abddc` |
| [NormalizationTest.txt](https://www.unicode.org/Public/16.0.0/ucd/NormalizationTest.txt) | `d811971453e7075e1ad56fb1b301eece5aa80757b81f6156e74a1bfb3ae5ceb1` |

Generation also reuses the vendored
[`UnicodeData.txt.gz`](../../unicodenames/ucd/UnicodeData.txt.gz), with digest
`ff58e5823bd095166564a006e47d111130813dcf8bf234ef79fa51a870edb48f`.
All inputs and generated tables use the shared
[Unicode License V3](../../unicodenames/ucd/LICENSE), already included in binary
archives. Compression uses `gzip.compress(data, compresslevel=9, mtime=0)`;
compressed bytes are not input identities.

Run from the repository root:

```sh
.venv/bin/python tools/generate_unicode_identifiers.py
.venv/bin/python tools/generate_unicode_identifiers.py --check --verify-cpython
go test ./internal/unicodeident
```

Generation is offline and independent of the interpreter's Unicode database.
`DerivedCoreProperties` supplies `XID_Start` and `XID_Continue`. `UnicodeData`
supplies canonical combining classes and recursive compatibility/canonical
decompositions; `Full_Composition_Exclusion` in `DerivedNormalizationProps`
determines which canonical decompositions may be inverted. Hangul decomposition
and composition follow the algorithm in
[Unicode UAX #15](https://www.unicode.org/reports/tr15/).

The generated binary contains sorted XID ranges and fixed-width lookup records,
with UTF-8 decomposition strings. It is about 120 KB and is embedded directly;
the production verifier does not need the source files, Python, or network.
Canonical ordering uses a stable sort of combining-mark runs, avoiding quadratic
insertion on long identifiers. ASCII normalization returns its input unchanged.

Go tests compare every code point's identifier properties against the source
data and test all five columns of the 19,965 official normalization vectors.
The optional CPython check requires CPython 3.14 with Unicode 16.0.0 and compares
all scalar identifier/combining properties plus the normalization corpus with
the interpreter. Application tests cover new Unicode 16.0 module, manifest,
parameter, and entrypoint names, source normalization, and cache equivalence.
