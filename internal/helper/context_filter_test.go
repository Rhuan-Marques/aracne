package helper

import (
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

func TestEffectiveContextFilterDefaults(t *testing.T) {
	c := &Config{}
	if c.EffectiveExternalVarsVisibility() != "normal" {
		t.Errorf("ext vars visibility default = %q, want normal", c.EffectiveExternalVarsVisibility())
	}
	if c.EffectiveSmallFunctionsVisibility() != "normal" {
		t.Errorf("small fn visibility default = %q, want normal", c.EffectiveSmallFunctionsVisibility())
	}
	if c.EffectiveSmallFunctionThreshold() != 5 {
		t.Errorf("threshold default = %d, want 5", c.EffectiveSmallFunctionThreshold())
	}
	if c.EffectiveIncludeIncoming() {
		t.Error("include incoming default should be false")
	}
	// An undescribed neighbour costs a CONTEXT line and answers the one question the section
	// exists to answer ("does this matter?") with nothing, so the default is to omit it.
	if !c.EffectiveHideNoDescription() {
		t.Error("hide no description default should be true")
	}

	f := c.EffectiveContextFilter()
	want := domain.DefaultContextFilter()
	if f != want {
		t.Errorf("EffectiveContextFilter() = %+v, want %+v", f, want)
	}
}

func TestEffectiveContextFilterOverrides(t *testing.T) {
	c := &Config{Read: ReadSection{ContextFilter: ContextFilterSection{
		IncludeIncoming:          true,
		ExternalVarsVisibility:   "hidden",
		SmallFunctionsVisibility: "full",
		SmallFunctionThreshold:   8,
		HideNoDescription:        boolPtr(true),
	}}}

	f := c.EffectiveContextFilter()
	if !f.IncludeIncoming {
		t.Error("IncludeIncoming should be true")
	}
	if f.ExtVarsVisibility != domain.VisibilityHidden {
		t.Errorf("ExtVarsVisibility = %d, want Hidden", f.ExtVarsVisibility)
	}
	if f.SmallFnVisibility != domain.VisibilityFull {
		t.Errorf("SmallFnVisibility = %d, want Full", f.SmallFnVisibility)
	}
	if f.SmallFnThreshold != 8 {
		t.Errorf("SmallFnThreshold = %d, want 8", f.SmallFnThreshold)
	}
	if !f.HideNoDescription {
		t.Error("HideNoDescription should be true")
	}
}

func TestNormalizeConfigContextFilter(t *testing.T) {
	c := &Config{Read: ReadSection{ContextFilter: ContextFilterSection{
		ExternalVarsVisibility:   "",
		SmallFunctionsVisibility: "bogus",
		SmallFunctionThreshold:   0,
	}}}
	normalizeConfig(c)
	if c.Read.ContextFilter.ExternalVarsVisibility != "normal" {
		t.Errorf("ext vars normalized = %q, want normal", c.Read.ContextFilter.ExternalVarsVisibility)
	}
	if c.Read.ContextFilter.SmallFunctionsVisibility != "normal" {
		t.Errorf("small fn normalized = %q, want normal", c.Read.ContextFilter.SmallFunctionsVisibility)
	}
	if c.Read.ContextFilter.SmallFunctionThreshold != 5 {
		t.Errorf("threshold normalized = %d, want 5", c.Read.ContextFilter.SmallFunctionThreshold)
	}
}

func TestDefaultConfigContextFilter(t *testing.T) {
	c := DefaultConfig()
	f := c.EffectiveContextFilter()
	if f != domain.DefaultContextFilter() {
		t.Errorf("default config filter = %+v, want default", f)
	}
}
