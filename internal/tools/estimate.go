package tools

import "encoding/json"

// bytesPerToken is the rule of thumb of every estimate in the program: about one token per four bytes. It suits any
// backend, and counting UTF-8 bytes keeps non-Latin text from being undercounted.
const bytesPerToken = 4

// EstimateTokens counts the tokens of the JSON of v.
func EstimateTokens(v any) int {
	raw, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return len(raw)/bytesPerToken + 1
}

// EstimateText counts the tokens of a text.
func EstimateText(text string) int { return len(text) / bytesPerToken }
