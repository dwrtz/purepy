# Pinned Unicode name inputs

The gzip files preserve the exact UTF-8 bytes published in the Unicode 16.0.0
Character Database. Their uncompressed SHA-256 digests are verified before
generation and by the Go tests:

| Input | SHA-256 |
| --- | --- |
| [UnicodeData.txt](https://www.unicode.org/Public/16.0.0/ucd/UnicodeData.txt) | `ff58e5823bd095166564a006e47d111130813dcf8bf234ef79fa51a870edb48f` |
| [NameAliases.txt](https://www.unicode.org/Public/16.0.0/ucd/NameAliases.txt) | `9953f0fcebf5ea8091c5c581e4df0e43f20d2533c84ccca7987a9bb819a896a8` |

[LICENSE](LICENSE) contains Unicode License V3. These inputs and the derived name
table are distributed under that license. Compression uses Python's
`gzip.compress(data, compresslevel=9, mtime=0)`; compressed bytes are not an input
identity because gzip implementations may differ.

Run from the repository root:

```sh
.venv/bin/python tools/generate_unicode_names.py
.venv/bin/python tools/generate_unicode_names.py --check --verify-cpython
```

Generation is offline and independent of the development interpreter's Unicode
database. The optional CPython check requires CPython 3.14 with Unicode 16.0.0
and checks all 155,475 accepted names and their lowercase variants using Python's
literal escape decoder. It also verifies that named sequences remain excluded.
It never imports or executes a project under verification.

The generated table stores 40,490 explicit names and aliases in sorted groups of
32. Each group begins with a complete name; subsequent entries hold a shared
prefix length and a suffix. Go lookups binary-search group heads, then scan at
most 32 entries using fixed 88-byte buffers. CJK and Tangut ranges come from
UnicodeData's `First`/`Last` records; Hangul names use the normative syllable
components. The production binary embeds only the derived name table, not the
source files, and needs no Python installation or network.

Name matching follows the corrected CPython 3.14 implementation: ASCII case is
ignored, spacing and hyphens remain exact, and algorithmic hexadecimal names
have no leading zero. Unicode named sequences are omitted because Python's
`\N{...}` literal decoder accepts single-character names and aliases only.
The primary implementation reference is
[CPython's Unicode lookup](https://github.com/python/cpython/blob/3.14/Modules/unicodedata.c),
especially `_getcode`, `parse_hex_code`, and `_check_alias_and_seq`. Earlier
CPython 3.14 patch releases had differences in derived-name lookup; the optional
CPython check fails if the development interpreter has those older behaviors.
