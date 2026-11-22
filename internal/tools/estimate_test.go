package tools

import "testing"

func TestEstimatesCountAboutFourBytesPerToken(t *testing.T) {
	tests := []struct {
		name string
		got  int
		want int
	}{
		{"text", EstimateText("123456789012"), 3}, // 12 bytes: 3 at four bytes a token, 4 at three
		{"an empty text", EstimateText(""), 0},
		{"multi-byte text counts its bytes", EstimateText("éééé"), 2},  // 8 bytes
		{"json adds its quotes and one", EstimateTokens("1234567"), 3}, // "1234567" is 9 bytes: 9/4 + 1
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %d, want %d", tt.got, tt.want)
			}
		})
	}
}
