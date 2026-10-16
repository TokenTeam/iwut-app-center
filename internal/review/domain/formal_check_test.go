package domain

import (
	"strings"
	"testing"
)

func TestBR_REV_016_FormalDefinitionBoundariesAndImmutability(t *testing.T) {
	id, _ := NewReviewCheckID("manual-check")
	for _, tc := range []struct {
		title, description string
		ok                 bool
	}{
		{"标题", "具体确认条件", true}, {strings.Repeat("界", 80), strings.Repeat("界", 2000), true},
		{"", "说明", false}, {"标题", "", false}, {" 标题", "说明", false}, {"标题", "说明\n", false},
		{strings.Repeat("界", 81), "说明", false}, {"标题", strings.Repeat("界", 2001), false}, {"标题", "坏\x00文本", false},
	} {
		_, err := NewFormalReviewCheckDefinition(id, tc.title, tc.description)
		if (err == nil) != tc.ok {
			t.Fatalf("formal text validity: %v", err)
		}
	}
	d, _ := NewFormalReviewCheckDefinition(id, "标题", "说明")
	p, err := NewVersionReviewPolicy("policy.v3", []ReviewCheckDefinition{d}, ReviewPolicyStatusActive)
	if err != nil {
		t.Fatal(err)
	}
	copy := p.RequiredChecks()
	copy[0] = ReviewCheckDefinition{}
	if p.RequiredChecks()[0].Title() != "标题" {
		t.Fatal("mutable policy definitions")
	}
	if _, err = p.ValidateConfirmation([]ReviewCheckID{id, id}); err == nil {
		t.Fatal("duplicate confirmation accepted")
	}
}
