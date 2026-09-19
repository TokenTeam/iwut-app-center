package domain

// unicodeCodePointLess compares valid UTF-8 by Unicode code point. The raw
// string tie-breaker keeps the order deterministic even for opaque external
// tokens containing malformed UTF-8, without inventing a ScopeName format.
func unicodeCodePointLess(left, right string) bool {
	leftRunes := []rune(left)
	rightRunes := []rune(right)
	limit := min(len(leftRunes), len(rightRunes))
	for index := 0; index < limit; index++ {
		if leftRunes[index] != rightRunes[index] {
			return leftRunes[index] < rightRunes[index]
		}
	}
	if len(leftRunes) != len(rightRunes) {
		return len(leftRunes) < len(rightRunes)
	}
	return left < right
}
