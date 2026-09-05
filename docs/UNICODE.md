# Unicode 16.0 identifiers and named escapes

PurePy pins Python 3.14's Unicode 16.0.0 identifier properties and NFKC
normalization. Source identifiers normalize before binding; module filenames,
configured entrypoints, and manifest names must already use canonical spelling.
For example, `Ᲊ` (U+1C89, CYRILLIC CAPITAL LETTER TJE) is a valid name, and newly
composed Kirat Rai vowel signs normalize consistently in source and configuration.
Names use Python's `XID_Start`/`XID_Continue` rules, including underscore; reserved
keywords remain prohibited where identifiers are required.

Module filenames also depend on the host filesystem. For example, the macOS 14
release runner rejects a filename containing U+1C89 with `EILSEQ`, before the
verifier can read it. Unicode source identifiers and manifest names remain
supported in files with ASCII names. The application suite always checks those
language boundaries; only the physical-filename case skips when file creation
returns `EILSEQ`. Other filesystem errors fail that test.

[`internal/unicodeident`](../internal/unicodeident/identifiers.go) embeds about
120 KB of generated identifier and normalization tables. Production behavior
does not depend on the Unicode version built into Go or installed Python.
ASCII normalization takes an allocation-free path. Non-ASCII identifiers use
recursive decomposition, stable canonical ordering, and canonical composition,
including the algorithmic Hangul rules. Input hashes, licensing, and generation
details are in the [identifier-data README](../internal/unicodeident/ucd/README.md).

## Named escapes

PurePy accepts `\N{...}` in ordinary Unicode strings and the literal portions of
f-strings. Names use Unicode 16.0.0, the database used by CPython 3.14. Character
names, single-character aliases, and algorithmic Hangul/CJK/Tangut names are
supported. Matching ignores ASCII case but preserves spaces and hyphens exactly.
Named sequences are excluded: Python's literal escape decoder accepts a single
character, even though `unicodedata.lookup` also supports sequences.

For example, these produce `"A"`, a NUL character, and a CJK character:

```python
"\N{LATIN CAPITAL LETTER A}"
"\N{nul}"
"\N{cjk unified ideograph-4e00}"
```

Unknown names, empty names, missing braces, noncanonical hexadecimal names, and
named sequences produce PP002. Diagnostics identify the original escape's byte
range and Unicode line/column coordinates. Raw strings and bytes preserve `\N`
literally; bytes still require ASCII source and valid hexadecimal escapes.
See [Python's lexical rules](https://docs.python.org/3.14/reference/lexical_analysis.html#named-unicode-character)
and [CPython's name lookup](https://github.com/python/cpython/blob/3.14/Modules/unicodedata.c).

## Data and production behavior

[`internal/unicodenames`](../internal/unicodenames/names.go) embeds a generated
membership table. It contains 40,490 explicit names and aliases, grouped for
binary search with shared prefixes. Each lookup scans at most 32 names and uses
fixed 88-byte buffers. Algorithmic names use the pinned code-point ranges and
Hangul syllable components. No Python interpreter, network, host Unicode data,
or analyzed-project execution is involved in production verification.

Tree-sitter misparses some ignored Unicode escapes in bytes. The frontend presents
bare bytes prefixes as raw prefixes to the grammar, keeping source length and
offsets unchanged. All lowered text and type classification use the original
source. Independent checks validate complete byte-literal boundaries, ASCII
contents, actual escapes, and statement separators. The parser cache identity
includes this lowering revision and the Unicode database version, so old cached
rejections are reparsed.

## Regeneration and checks

```sh
make unicode-generate
make unicode-test
make syntax-test differential-test
```

These Makefile targets set up `.venv` with `uv`. Generation itself is offline and
uses only the vendored compressed Unicode data; it checks the SHA-256 of each
uncompressed input before generating deterministic files. Input URLs, hashes,
storage details, and the Unicode license are in the
[pinned-data README](../internal/unicodenames/ucd/README.md).
Local binary archives include the Unicode notice at `licenses/UNICODE.txt`.

`make unicode-test` checks that generated files are current, compares all 155,475
accepted names and their lowercase variants with CPython's literal escape decoder,
checks rejection boundaries, and runs the Go lookup tests. The Python comparison
requires CPython 3.14 with Unicode 16.0.0; it was validated with CPython 3.14.7.
Earlier 3.14 patch releases had differences in algorithmic name lookup; a
disagreement fails the gate instead of silently changing the generated table.

The same target regenerates and checks the pinned identifier data, compares all
Unicode code points' identifier and combining properties with CPython 3.14, and
verifies all 19,965 official normalization vectors against both Go and CPython.
Application tests cover Unicode 16.0 names across project discovery, configuration,
manifest linking, source normalization, and cold/warm/disabled cache runs.

Go tests check all explicit source names/aliases, Hangul syllables, and CJK/Tangut
range points, plus malformed names and range boundaries. Frontend and application
tests cover prefixes, adjacency, f-string nesting, exact error ranges, and
cold/warm/disabled caches. Shared syntax fixtures compare valid and malformed
spellings with CPython's AST parser, while semantic cases assert the inferred
type and independently specified Python value. These finite source combinations
do not prove complete Python parser equivalence.
