# ADR 0001: Python syntax, PurePy semantics

Status: accepted for PurePy 0.1.

PurePy parses a Python 3.14 subset and assigns closed semantics only to explicitly
admitted forms. The verifier never executes source. Parser-specific objects stay
inside the frontend; lowering produces the independent semantic model. Every
unknown form or operation produces an error.

This rejects adopting Python's full data model, treating parse success as semantic
approval, or trusting annotations to constrain arbitrary runtime dispatch. Those
alternatives would admit descriptors, operator methods, mutable globals and other
ambient behavior without an explicit authorization boundary. Exact built-in
operations and verified data records keep the accepted semantics reviewable.

Reopen only with a concrete useful program that cannot be expressed through the
current subset, a complete semantic rule that preserves purity, positive and
adversarial conformance fixtures, and runtime-parity evidence. Parser convenience
or broad Python compatibility alone is insufficient evidence.
