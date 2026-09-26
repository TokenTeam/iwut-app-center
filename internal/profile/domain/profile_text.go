package domain

import (
	"sort"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// These values are plain text. In particular, icon has no URL or asset meaning.
type ApplicationDisplayName struct{ value string }
type ApplicationDescription struct{ value string }
type ApplicationIcon struct{ value string }

func NewApplicationDisplayName(value string) (ApplicationDisplayName, error) {
	normalized, ok := normalizeText(value, 80)
	if !ok {
		return ApplicationDisplayName{}, ErrInvalidApplicationDisplayName
	}
	return ApplicationDisplayName{normalized}, nil
}
func NewApplicationDescription(value string) (ApplicationDescription, error) {
	normalized, ok := normalizeText(value, 1000)
	if !ok {
		return ApplicationDescription{}, ErrInvalidApplicationDescription
	}
	return ApplicationDescription{normalized}, nil
}
func NewApplicationIcon(value string) (ApplicationIcon, error) {
	normalized, ok := normalizeText(value, 512)
	if !ok {
		return ApplicationIcon{}, ErrInvalidApplicationIcon
	}
	return ApplicationIcon{normalized}, nil
}
func (v ApplicationDisplayName) String() string { return v.value }
func (v ApplicationDescription) String() string { return v.value }
func (v ApplicationIcon) String() string        { return v.value }
func normalizeText(value string, max int) (string, bool) {
	if !utf8.ValidString(value) {
		return "", false
	}
	normalized := canonicalNFC(value)
	if !canonicalText(normalized, max) {
		return "", false
	}
	return normalized, true
}
func canonicalText(value string, max int) bool {
	if !utf8.ValidString(value) || canonicalNFC(value) != value {
		return false
	}
	size := utf8.RuneCountInString(value)
	if size < 1 || size > max {
		return false
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	if unicode.IsSpace(first) || unicode.IsSpace(last) {
		return false
	}
	for _, r := range value {
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Cs, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return false
		}
	}
	return true
}
func cloneDescription(value *ApplicationDescription) *ApplicationDescription {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func cloneIcon(value *ApplicationIcon) *ApplicationIcon {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// canonicalNFC uses norm's Unicode tables without its stream-safe transform,
// which inserts CGJ after 30 non-starters. The business contract requires NFC
// and preserves the caller's code points, including any original CGJ.
func canonicalNFC(value string) string {
	type character struct {
		r   rune
		ccc uint8
	}
	chars := make([]character, 0, utf8.RuneCountInString(value))
	for _, r := range value {
		// A single scalar's canonical decomposition cannot reach the stream-safe
		// limit; decomposing scalars separately therefore introduces no characters.
		for _, d := range norm.NFD.String(string(r)) {
			chars = append(chars, character{d, norm.NFD.PropertiesString(string(d)).CCC()})
		}
	}
	// Canonical ordering is stable within each run of non-starters.
	for start := 0; start < len(chars); {
		if chars[start].ccc == 0 {
			start++
			continue
		}
		end := start + 1
		for end < len(chars) && chars[end].ccc != 0 {
			end++
		}
		sort.SliceStable(chars[start:end], func(i, j int) bool { return chars[start+i].ccc < chars[start+j].ccc })
		start = end
	}
	result := make([]rune, 0, len(chars))
	starter := -1
	var lastCCC uint8
	for _, ch := range chars {
		if starter >= 0 && (lastCCC == 0 || lastCCC < ch.ccc) {
			pair := norm.NFC.String(string([]rune{result[starter], ch.r}))
			if utf8.RuneCountInString(pair) == 1 {
				result[starter], _ = utf8.DecodeRuneInString(pair)
				continue
			}
		}
		if ch.ccc == 0 {
			starter = len(result)
		}
		result = append(result, ch.r)
		lastCCC = ch.ccc
	}
	return string(result)
}
