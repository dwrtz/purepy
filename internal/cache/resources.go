package cache

const (
	maxCacheJSONValues      = 2_000_000
	maxDiagnosticJSONValues = 8_192
)

// withinJSONBudget bounds allocations before encoding/json constructs typed
// slices/maps. Tiny objects in malformed diagnostics otherwise amplify a
// 300 KiB cache file into over 100 MiB of allocations before validation runs.
// Count structural value boundaries outside strings without allocating; full
// JSON syntax validation remains encoding/json's responsibility. Bounds on
// nodes/depth/diagnostic shape are still checked after decoding as well.
func withinJSONBudget(data []byte, remaining int) bool {
	quoted, escaped, depth := false, false, 0
	for _, b := range data {
		if quoted {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				quoted = false
			}
			continue
		}
		switch b {
		case '"':
			quoted = true
		case '{', '[':
			remaining--
			depth++
		case '}', ']':
			depth--
		case ',', ':':
			remaining--
		}
		if remaining < 0 || depth > 2*maxNodeDepth+16 {
			return false
		}
	}
	return true
}
