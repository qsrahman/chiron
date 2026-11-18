package tools

import "encoding/json"

// EstimateTokens counts about one token per four bytes of the JSON of v. That is tokenizer-agnostic, so it suits any
// backend, and counting UTF-8 bytes keeps non-Latin text from being undercounted.
func EstimateTokens(v any) int {
	raw, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return len(raw)/4 + 1
}
