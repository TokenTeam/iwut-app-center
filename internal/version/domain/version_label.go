package domain

import (
	"unicode"
	"unicode/utf8"
)

const MaximumVersionLabelCodePoints = 50

type VersionLabel string

func NewVersionLabel(value string) (VersionLabel, error) {
	if !utf8.ValidString(value) {
		return "", ErrInvalidVersionLabel
	}
	runes := []rune(value)
	if len(runes) == 0 || len(runes) > MaximumVersionLabelCodePoints {
		return "", ErrInvalidVersionLabel
	}
	if unicode.IsSpace(runes[0]) || unicode.IsSpace(runes[len(runes)-1]) {
		return "", ErrInvalidVersionLabel
	}
	for _, value := range runes {
		if unicode.Is(unicode.Cc, value) {
			return "", ErrInvalidVersionLabel
		}
	}
	return VersionLabel(value), nil
}

func (label VersionLabel) String() string { return string(label) }

func (label VersionLabel) valid() bool {
	validated, err := NewVersionLabel(string(label))
	return err == nil && validated == label
}
