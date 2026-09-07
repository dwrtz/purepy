# ADR 0003: Capability parameter authorization

Status: accepted for PurePy 0.2.

External authority enters a verified function through explicit nominal capability
parameters. A known operation receives the original parameter directly at the
matching exact type. Capabilities cannot be constructed, aliased, returned,
stored, compared or inspected. Labels report exact authority; there is no
hierarchy, wildcard implication or ambient lookup. Host references carry no
authority by themselves.

This rejects inferred global effect sets, context variables, dependency-injection
containers, registries and source annotations that authorize effects without an
incoming token. Those mechanisms hide the route by which a function receives
authority. Direct parameter dataflow makes each authorized call locally auditable.

Reopen only with real signatures demonstrating a blocking usability problem and
a proposal whose authority provenance remains explicit and non-forgeable. The
proposal must prove capability nonescape, exact call authorization and absence
of ambient acquisition, with negative fixtures for each storage and alias path.
