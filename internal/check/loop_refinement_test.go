package check

import "testing"

func TestForTargetRefinementsRespectEveryExit(t *testing.T) {
	tests := []struct{ name, source, code string }{
		{
			"break_keeps_optional_target",
			`def f(xs: tuple[int | None, ...]) -> int:
    x: int | None = 1
    if x is None:
        return 0
    for x in xs:
        if x is None:
            break
    return x
`, "PP205",
		},
		{
			"continue_keeps_last_optional_target",
			`def f(xs: tuple[int | None, ...]) -> int:
    x: int | None = 1
    if x is None:
        return 0
    for x in xs:
        if x is None:
            continue
    return x
`, "PP205",
		},
		{
			"zero_iterations_leave_target_unassigned",
			`def f(xs: tuple[int, ...]) -> int:
    for x in xs:
        pass
    return x
`, "PP206",
		},
		{
			"zero_iterations_keep_initial_none",
			`def f(xs: tuple[int, ...]) -> int:
    x: int | None = None
    for x in xs:
        if x is None:
            return 0
    return x
`, "PP205",
		},
		{
			"preassigned_exact_target_survives_zero_iterations",
			`def f(xs: tuple[int, ...]) -> int:
    x = 7
    for x in xs:
        pass
    return x
`, "",
		},
		{
			"optional_target_can_be_narrowed_after_loop",
			`def f(xs: tuple[int | None, ...]) -> int:
    x: int | None = 1
    for x in xs:
        if x is None:
            break
    if x is None:
        return 0
    return x
`, "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { specCoreCheck(t, tt.source, tt.code) })
	}
}

func TestForIterableUsesIncomingRefinements(t *testing.T) {
	tests := []struct{ name, source, code string }{
		{
			"sequence_evaluated_before_body_rebinding",
			`def f(xs: tuple[int, ...] | None) -> int:
    if xs is None:
        return 0
    total = 0
    for x in xs:
        xs = None
        total = total + x
    return total
`, "",
		},
		{
			"range_bound_evaluated_before_body_rebinding",
			`def f(n: int | None) -> int:
    if n is None:
        return 0
    total = 0
    for x in range(n):
        n = None
        total = total + x
    return total
`, "",
		},
		{
			"body_still_accounts_for_previous_iterations",
			`def f(xs: tuple[int, ...] | None) -> int:
    if xs is None:
        return 0
    total = 0
    for x in xs:
        total = total + len(xs)
        xs = None
    return total
`, "PP303",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { specCoreCheck(t, tt.source, tt.code) })
	}
}

func TestWhileConditionUsesBackEdgeTypes(t *testing.T) {
	tests := []struct{ name, source, code string }{
		{
			"repeated_condition_cannot_use_initial_refinement",
			`def f(x: int | None) -> None:
    if x is None:
        return
    while x > 0:
        x = None
`, "PP212",
		},
		{
			"condition_narrows_again_on_each_iteration",
			`def f(x: int | None) -> int:
    total = 0
    while x is not None:
        total = total + x
        x = None
    return total
`, "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { specCoreCheck(t, tt.source, tt.code) })
	}
}
