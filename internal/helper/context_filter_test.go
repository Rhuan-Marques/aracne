package helper

import (
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// A bare config renders the default context block, and so does the explicit "normal" preset.
func TestContextFilterDefaultsToNormal(t *testing.T) {
	for _, c := range []*Config{{}, {Read: ReadSection{ContextFilter: ContextFilterNormal}}} {
		if got, want := c.EffectiveContextFilter(), domain.DefaultContextFilter(); got != want {
			t.Errorf("EffectiveContextFilter() = %+v, want %+v", got, want)
		}
		// An undescribed neighbour costs a CONTEXT line and answers the one question the
		// section exists to answer ("does this matter?") with nothing, so it is omitted.
		if !c.EffectiveContextFilter().HideNoDescription {
			t.Error("hide-no-description should be on by default")
		}
		if c.EffectiveIncludeIncoming() {
			t.Error(`"# USED BY:" should be off by default`)
		}
	}
}

// "off" renders the code asked for and nothing around it.
func TestContextFilterOffHidesEveryNeighbour(t *testing.T) {
	c := &Config{Read: ReadSection{ContextFilter: ContextFilterOff}}
	f := c.EffectiveContextFilter()
	if f.ExtVarsVisibility != domain.VisibilityHidden {
		t.Errorf("ExtVarsVisibility = %d, want Hidden", f.ExtVarsVisibility)
	}
	if f.SmallFnVisibility != domain.VisibilityHidden {
		t.Errorf("SmallFnVisibility = %d, want Hidden", f.SmallFnVisibility)
	}
	if f.IncludeIncoming {
		t.Error(`"off" must not add "# USED BY:"`)
	}
}

// "full" is the other end: neighbours as fenced cuts, undescribed ones kept, plus "# USED BY:".
func TestContextFilterFullElevatesEveryNeighbour(t *testing.T) {
	c := &Config{Read: ReadSection{ContextFilter: ContextFilterFull}}
	f := c.EffectiveContextFilter()
	if f.ExtVarsVisibility != domain.VisibilityFull {
		t.Errorf("ExtVarsVisibility = %d, want Full", f.ExtVarsVisibility)
	}
	if f.SmallFnVisibility != domain.VisibilityFull {
		t.Errorf("SmallFnVisibility = %d, want Full", f.SmallFnVisibility)
	}
	if f.HideNoDescription {
		t.Error(`"full" must keep undescribed neighbours`)
	}
	if !c.EffectiveIncludeIncoming() {
		t.Error(`"full" should add "# USED BY:"`)
	}
}

// A typo must not silently render an EMPTY context block -- that would strip every neighbour
// from every read while looking like a configuration that worked.
func TestAnUnknownContextFilterFallsBackToTheDefault(t *testing.T) {
	c := &Config{Read: ReadSection{ContextFilter: "verbose"}}
	if got, want := c.EffectiveContextFilter(), domain.DefaultContextFilter(); got != want {
		t.Errorf("unknown preset = %+v, want the default %+v", got, want)
	}
	if err := c.Validate(); err == nil {
		t.Error("Validate should reject an unknown read.context_filter")
	}
}

// normalizeConfig stamps the resolved preset, so a re-saved config states its verbosity.
func TestNormalizeConfigStampsTheContextFilter(t *testing.T) {
	for in, want := range map[string]string{
		"":        ContextFilterNormal,
		"bogus":   ContextFilterNormal,
		"  FULL ": ContextFilterFull,
		"off":     ContextFilterOff,
	} {
		c := &Config{Read: ReadSection{ContextFilter: in}}
		normalizeConfig(c)
		if c.Read.ContextFilter != want {
			t.Errorf("normalize(%q) = %q, want %q", in, c.Read.ContextFilter, want)
		}
	}
}

func TestDefaultConfigContextFilter(t *testing.T) {
	if got, want := DefaultConfig().EffectiveContextFilter(), domain.DefaultContextFilter(); got != want {
		t.Errorf("default config filter = %+v, want %+v", got, want)
	}
}
