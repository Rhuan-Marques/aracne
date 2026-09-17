package warnread

import (
	"strings"
	"testing"
)

// sizedPages fakes a report whose page for k warnings is sizes[k-1] bytes long, and counts how
// many pages were rendered -- each one is a full batched read in production.
func sizedPages(sizes []int) (func(int) string, *int) {
	probes := 0
	return func(k int) string {
		probes++
		return strings.Repeat("x", sizes[k-1])
	}, &probes
}

func firstReadable() int { return 1 }

// The case the budget exists for: 6 warnings come to 8 KB and 7 to 11 KB, so a 10 KB budget
// shows 6. Not 5 because it is safe, not 7 because the note is small.
func TestFitPrefixShowsTheLongestPageThatFits(t *testing.T) {
	page, _ := sizedPages([]int{1000, 2500, 4000, 5500, 7000, 8000, 11000, 12500, 14000})
	if got := len(fitPrefix(9, 10000, firstReadable, page)); got != 8000 {
		t.Fatalf("fitted a %d-byte page, want the 8000-byte page of 6 warnings", got)
	}
}

// Exactly at the limit fits; one byte over does not.
func TestFitPrefixBoundaryIsInclusive(t *testing.T) {
	page, _ := sizedPages([]int{100, 200, 300})
	if got := len(fitPrefix(3, 200, firstReadable, page)); got != 200 {
		t.Errorf("limit 200 fitted %d, want 200", got)
	}
	if got := len(fitPrefix(3, 199, firstReadable, page)); got != 100 {
		t.Errorf("limit 199 fitted %d, want 100", got)
	}
}

// The common case costs one render: a report that fits is never searched.
func TestFitPrefixRendersOnceWhenEverythingFits(t *testing.T) {
	page, probes := sizedPages([]int{100, 200, 300})
	if got := len(fitPrefix(3, 10000, firstReadable, page)); got != 300 {
		t.Fatalf("fitted %d, want all 300", got)
	}
	if *probes != 1 {
		t.Errorf("rendered %d pages for a report that fits, want 1", *probes)
	}
}

// Logarithmic, not linear: a thousand warnings must not mean a thousand batched reads.
func TestFitPrefixProbesLogarithmically(t *testing.T) {
	sizes := make([]int, 1000)
	for i := range sizes {
		sizes[i] = (i + 1) * 10
	}
	for _, limit := range []int{15, 4321, 9989, 5000} {
		page, probes := sizedPages(sizes)
		got := len(fitPrefix(len(sizes), limit, firstReadable, page))
		if want := limit / 10 * 10; got != want {
			t.Errorf("limit %d: fitted %d, want %d", limit, got, want)
		}
		if *probes > 12 { // the full try, then ceil(log2 1000) = 10
			t.Errorf("limit %d: %d renders, want at most 12", limit, *probes)
		}
	}
}

// Unlimited is the whole report whatever its size.
func TestFitPrefixZeroIsUnlimited(t *testing.T) {
	page, _ := sizedPages([]int{5000, 50000})
	if got := len(fitPrefix(2, 0, firstReadable, page)); got != 50000 {
		t.Fatalf("limit 0 fitted %d, want everything", got)
	}
}

// Not even one warning fits: no page, so the caller falls back to its (also fitted) listing.
// A negative limit is what a reserve larger than the budget resolves to.
func TestFitPrefixNothingFits(t *testing.T) {
	for _, limit := range []int{50, -1} {
		page, _ := sizedPages([]int{100, 200})
		if got := fitPrefix(2, limit, firstReadable, page); got != "" {
			t.Errorf("limit %d: got a %d-byte page, want none", limit, len(got))
		}
	}
}

// Warnings with nothing to read in front: their pages are empty, which must not read as "too
// big" and send the search below the first page that has code in it.
func TestFitPrefixStartsAtTheFirstReadableWarning(t *testing.T) {
	sizes := []int{0, 0, 0, 400, 600, 900}
	page, _ := sizedPages(sizes)
	got := fitPrefix(6, 700, func() int { return 4 }, page)
	if len(got) != 600 {
		t.Fatalf("fitted %d, want the 600-byte page", len(got))
	}
	if fitPrefix(6, 700, func() int { return 0 }, page) != "" {
		t.Error("nothing is readable, so there is no page")
	}
}
