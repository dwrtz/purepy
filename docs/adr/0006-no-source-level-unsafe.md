# ADR 0006: No source-level unsafe escape

Status: accepted for PurePy 0.1.

Every source file under the configured root is verified under the same closed
rules. There are no unsafe decorators, trusted-function bodies, suppression
comments or per-file exclusions. Unverifiable implementations live outside the
source root and expose a narrow declarative host manifest.

This rejects unchecked source blocks and annotations that silently turn unknown
behavior into trusted behavior. Such exceptions create hidden holes in a
repository's verification claim. A visible filesystem and manifest boundary
lets reviewers identify the trusted code and the authority each operation needs.

Reopen only with evidence that a required host boundary cannot be expressed by
the existing source-root separation and manifest format. Any proposal must keep
all trust assertions explicit in reports, preserve a precise acceptance claim,
and include abuse cases showing that unsupported source cannot acquire ambient
authority through a suppression mechanism.
