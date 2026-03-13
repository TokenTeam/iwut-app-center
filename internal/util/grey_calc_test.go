package util

import (
	"math"
	"testing"
)

func TestGreyCalc_HitRateApprox_UUIDv7(t *testing.T) {
	g := NewGreyCalc()
	shuffleCode := g.GetRandomGreyShuffleCode()

	greyRate := 0.85
	const n = 1000000

	hits := 0
	for i := 0; i < n; i++ {
		uid := MustUUIDv7String()
		ok, err := g.IsUseGrey(uid, shuffleCode, greyRate)
		if err != nil {
			t.Fatalf("IsUseGrey returned error: %v", err)
		}
		if ok {
			hits++
		}
	}

	rate := float64(hits) / float64(n)

	// For Binomial(n, p): stddev of sample proportion = sqrt(p(1-p)/n)
	// Use a wide 6-sigma window to reduce flakiness.
	sigma := math.Sqrt(greyRate * (1.0 - greyRate) / float64(n))
	lower := greyRate - 6.0*sigma
	upper := greyRate + 6.0*sigma
	if lower < 0 {
		lower = 0
	}
	if upper > 1 {
		upper = 1
	}

	if rate < lower || rate > upper {
		t.Fatalf("hit rate out of range: hits=%d n=%d rate=%.4f want in [%.4f, %.4f] greyRate=%.2f shuffleCode=%d", hits, n, rate, lower, upper, greyRate, shuffleCode)
	}
}

func TestGreyCalc_GreyRateBounds(t *testing.T) {
	g := NewGreyCalc()
	shuffleCode := g.GetRandomGreyShuffleCode()
	uid := MustUUIDv7String()

	ok, err := g.IsUseGrey(uid, shuffleCode, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("greyRate=0 should never hit")
	}

	ok, err = g.IsUseGrey(uid, shuffleCode, -0.1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("greyRate<0 should never hit")
	}

	ok, err = g.IsUseGrey(uid, shuffleCode, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("greyRate=1 should always hit")
	}

	ok, err = g.IsUseGrey(uid, shuffleCode, 1.5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("greyRate>1 should always hit")
	}
}

func TestGreyCalc_EmptyUID(t *testing.T) {
	g := NewGreyCalc()
	shuffleCode := g.GetRandomGreyShuffleCode()

	ok, err := g.IsUseGrey("", shuffleCode, 0.5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("empty uid should never hit")
	}
}

func TestGreyCalc_TableMissReturnsError(t *testing.T) {
	g := NewGreyCalc()

	// Pick a shuffle number that's guaranteed not in the precomputed 8-digit base-8 permutations.
	// The generator only uses digits [0..7] in each 3-bit slot; using all 7s repeats digits and won't exist.
	const invalidShuffle uint32 = 0xFFFFFF // 24 bits set => 8 digits all 7

	uid := MustUUIDv7String()
	_, err := g.IsUseGrey(uid, invalidShuffle, 0.5)
	if err == nil {
		t.Fatalf("expected error for table miss, got nil")
	}
}
