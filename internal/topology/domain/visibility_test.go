package domain

import "testing"

func TestParseVisibility(t *testing.T) {
	cases := map[string]Visibility{
		"hidden":  VisibilityHidden,
		"normal":  VisibilityNormal,
		"full":    VisibilityFull,
		"FULL":    VisibilityFull,
		" Hidden": VisibilityHidden,
		"":        VisibilityNormal,
		"bogus":   VisibilityNormal,
	}
	for in, want := range cases {
		if got := ParseVisibility(in); got != want {
			t.Errorf("ParseVisibility(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestVisibilityMax(t *testing.T) {
	if VisibilityNormal.Max(VisibilityFull) != VisibilityFull {
		t.Error("Max(Normal, Full) should be Full")
	}
	if VisibilityHidden.Max(VisibilityNormal) != VisibilityNormal {
		t.Error("Max(Hidden, Normal) should be Normal")
	}
	if VisibilityFull.Max(VisibilityHidden) != VisibilityFull {
		t.Error("Max(Full, Hidden) should be Full")
	}
}

func TestContextFilterFor(t *testing.T) {
	full := ContextFilter{
		ExtVarsVisibility: VisibilityFull,
		SmallFnVisibility: VisibilityFull,
		SmallFnThreshold:  5,
	}
	// Small function (<= threshold) becomes Full.
	if got := full.For(ResourceFunction, 3, true); got != VisibilityFull {
		t.Errorf("small function = %d, want Full", got)
	}
	// Large function stays Normal.
	if got := full.For(ResourceFunction, 50, true); got != VisibilityNormal {
		t.Errorf("large function = %d, want Normal", got)
	}
	// External variable follows ExtVarsVisibility.
	if got := full.For(ResourceVariable, 0, true); got != VisibilityFull {
		t.Errorf("ext var = %d, want Full", got)
	}
	// A struct/type is always Normal by kind.
	if got := full.For(ResourceType, 0, true); got != VisibilityNormal {
		t.Errorf("type = %d, want Normal", got)
	}
}

func TestContextFilterHideNoDescription(t *testing.T) {
	f := ContextFilter{
		ExtVarsVisibility: VisibilityNormal,
		SmallFnVisibility: VisibilityFull,
		SmallFnThreshold:  5,
		HideNoDescription: true,
	}
	// Normal resource without description is hidden.
	if got := f.For(ResourceType, 0, false); got != VisibilityHidden {
		t.Errorf("no-desc Normal = %d, want Hidden", got)
	}
	// With a description it stays Normal.
	if got := f.For(ResourceType, 0, true); got != VisibilityNormal {
		t.Errorf("described Normal = %d, want Normal", got)
	}
	// A small function resolves Full and is exempt from hide_no_description.
	if got := f.For(ResourceFunction, 2, false); got != VisibilityFull {
		t.Errorf("no-desc small function = %d, want Full (exempt)", got)
	}
}
