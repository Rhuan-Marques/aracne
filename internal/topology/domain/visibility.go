package domain

import "strings"

// Visibility controls how a neighbor resource is rendered inside the
// "# CONTEXT:" block of a read result. Higher values win when the same
// resource is reachable at multiple visibilities (see ContextFilter docs).
type Visibility int

// VisibilityHidden omits the resource entirely.
// VisibilityFull renders the resource as a fenced source-code cut.
// VisibilityNormal renders the resource as "ID: description" (the default).
const (
	// VisibilityHidden omits the resource entirely.
	VisibilityHidden Visibility = 1
	// VisibilityNormal renders the resource as "ID: description" (the default).
	VisibilityNormal Visibility = 2
	// VisibilityFull renders the resource as a fenced source-code cut.
	VisibilityFull Visibility = 3
)

// ParseVisibility maps a config string to a Visibility, defaulting to
// VisibilityNormal for empty or unrecognized values.
func ParseVisibility(s string) Visibility {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "hidden":
		return VisibilityHidden
	case "full":
		return VisibilityFull
	default:
		return VisibilityNormal
	}
}

// Max returns the higher of two visibilities.
func (v Visibility) Max(other Visibility) Visibility {
	if other > v {
		return other
	}
	return v
}

// DefaultMaxInlineParentLines is the default ceiling for inlining a method's enclosing type.
// Big enough for an ordinary Go struct or a small class, small enough that a 500-line Python
// class never rides along with one of its methods.
const DefaultMaxInlineParentLines = 40

// ContextFilter is the resolved read.context_filter configuration. It decides,
// per neighbor, whether it is hidden, rendered normally, or rendered in full.
type ContextFilter struct {
	// IncludeIncoming adds a "# USED BY:" section listing the resources that
	// call/use the resource being read.
	IncludeIncoming bool
	// ExtVarsVisibility is the visibility applied to external variables.
	ExtVarsVisibility Visibility
	// SmallFnVisibility is the visibility applied to functions/methods whose
	// line count is within SmallFnThreshold.
	SmallFnVisibility Visibility
	// SmallFnThreshold is the inclusive line-count ceiling for a "small"
	// function. Ignored when SmallFnVisibility is Normal.
	SmallFnThreshold int
	// HideNoDescription hides resources that would render Normal but have no
	// description. Full resources are exempt (they show code, not a description).
	HideNoDescription bool
	// MaxInlineParentLines is the line-count ceiling for inlining a method's enclosing
	// type above the method body.
	//
	// Reading a method has always shown its receiver so the code makes sense on its own. For
	// Go and Rust that is a short type declaration. For Python, JS and Java the "class cut"
	// is the ENTIRE class, so reading one method of a large class returned the whole class --
	// often the whole file. Past this ceiling the parent drops to an ordinary Normal
	// neighbour: named, described, and readable by ID if the model actually wants it.
	// 0 disables inlining entirely; a negative value means no ceiling.
	MaxInlineParentLines int
}

// InlineParent reports whether an enclosing type spanning lineCount lines is small enough to
// print above the member being read.
func (f ContextFilter) InlineParent(lineCount int) bool {
	if f.MaxInlineParentLines < 0 {
		return true
	}
	if lineCount <= 0 {
		// An unknown span is the pre-existing behaviour's benefit of the doubt: a cut we
		// cannot measure is almost always a short one-line declaration.
		return f.MaxInlineParentLines != 0
	}
	return lineCount <= f.MaxInlineParentLines
}

// DefaultContextFilter returns the all-Normal filter (current behavior): no
// hiding, no full cuts, no incoming section.
func DefaultContextFilter() ContextFilter {
	return ContextFilter{
		ExtVarsVisibility:    VisibilityNormal,
		SmallFnVisibility:    VisibilityNormal,
		SmallFnThreshold:     5,
		MaxInlineParentLines: DefaultMaxInlineParentLines,
	}
}

// For computes the visibility of a single neighbor of the given kind. lineCount
// is the neighbor's source line span (EndsAt-StartsAt+1); pass 0 when unknown.
// hasDescription reports whether the neighbor carries a non-empty description.
func (f ContextFilter) For(kind ResourceKind, lineCount int, hasDescription bool) Visibility {
	vis := VisibilityNormal
	switch kind {
	case ResourceVariable:
		vis = f.ExtVarsVisibility
	case ResourceFunction, ResourceMethod:
		if lineCount > 0 && lineCount <= f.SmallFnThreshold {
			vis = f.SmallFnVisibility
		}
	}
	// hide_no_description only suppresses resources rendered Normal; Full
	// resources display the actual code so they stay.
	if f.HideNoDescription && !hasDescription && vis == VisibilityNormal {
		vis = VisibilityHidden
	}
	return vis
}

// ResourceRef is a lightweight, language-agnostic reference to a resource,
// used for the always-Normal "# USED BY:" (incoming connections) section.
type ResourceRef struct {
	ID          string
	Kind        ResourceKind
	Name        string
	Description string
	Location    Location
}
