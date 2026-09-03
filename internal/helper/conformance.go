package helper

import (
	"fmt"

	"aracne/internal/topology/contract"
	"aracne/internal/topology/domain"
)

// conformanceEdges are the two ways a type declares that it must provide an interface's
// methods. "implements" is Rust's `impl Trait for Type`, TypeScript's `class C implements
// I` and Java's `implements`; "inherits" additionally covers Python, whose abstract base
// class is an ordinary superclass rather than a separate interface node.
var conformanceEdges = []string{"implements", "inherits"}

// InterfaceConflictWarnings reports every type that DECLARES it implements an interface
// and no longer does.
//
// This is the other half of what a signature change breaks. The call-site rule catches a
// caller that passes the wrong arguments; nothing caught the type that promised to provide
// a method and stopped providing it. Widening one interface method silently unhooks every
// implementer, and the only sign is a build failure somewhere else entirely.
//
// Derived from current state, never from history, so it appears when the conflict appears
// and disappears when it is fixed -- the same property that makes the call-site rule
// immune to the revert case. Nothing is stored and nothing has to be cleared.
//
// It runs only where the declaration is a CLAIM that can be wrong. Go and Python's Protocol
// are structural: their implements edge is DERIVED from satisfaction, so a broken type
// simply has no edge, and reporting one would mean guessing which types were meant to
// implement which interfaces. See contract.ChecksConformance.
func InterfaceConflictWarnings(topo *domain.Topology) []domain.TopologyWarning {
	if topo == nil {
		return nil
	}
	var out []domain.TopologyWarning
	for implID, impl := range topo.Resources {
		if !contract.ChecksConformance(impl.Language) {
			continue
		}
		// An interface extending another -- TypeScript's `interface Solid extends Shape`,
		// Rust's `trait Drawable: Shape` -- PROPAGATES the requirement to whoever
		// implements it. It is not itself an implementer and has no methods to provide.
		if impl.Kind == domain.ResourceInterface {
			continue
		}
		for _, edge := range conformanceEdges {
			for _, ifaceID := range impl.Connections[edge] {
				iface, ok := topo.Resources[ifaceID]
				if !ok || iface.Language != impl.Language {
					continue
				}
				// Rust's `#[derive(Trait)]` has the compiler write the impl, so the methods
				// exist in the built crate and nowhere in the source. There is nothing to
				// read and nothing that can be wrong.
				if isDerived(impl, iface.Name) {
					continue
				}
				out = append(out, conflictsFor(topo, implID, impl, ifaceID, iface)...)
			}
		}
	}
	return out
}

// conflictsFor checks one declared relationship.
func conflictsFor(
	topo *domain.Topology,
	implID string, impl domain.Resource,
	ifaceID string, iface domain.Resource,
) []domain.TopologyWarning {
	required := requiredMethods(topo, iface)
	if len(required) == 0 {
		return nil
	}
	provided := providedMethods(topo, impl)
	// An intermediate abstract class may legitimately leave requirements unmet -- that is
	// what makes it abstract. Only a type that declares nothing abstract of its own is
	// promising to be instantiable, and only that promise can be broken.
	if declaresAbstract(provided) {
		return nil
	}

	var out []domain.TopologyWarning
	for _, req := range required {
		if req.Name == "" || req.HasDefault {
			continue // the interface supplies a body; an implementer need not
		}
		candidates := concreteOnly(provided[req.Name])
		if len(candidates) == 0 {
			// Inheritance chains mean a superclass may satisfy it; only report when
			// nothing up the chain provides the name.
			if inheritedMethod(topo, impl, req.Name, 0) {
				continue
			}
			out = append(out, conflictWarning(implID, impl, ifaceID, iface, req.Name,
				fmt.Sprintf("%s declares it implements %s but does not provide %s",
					impl.Name, iface.Name, req.Name)))
			continue
		}
		// Overloads mean one name can have several methods, and only ONE of them has to
		// fit. Java is the obvious case -- a class may declare area() and area(int), and
		// area() is the override -- but reporting the first match found would make the
		// verdict depend on map iteration order, which is a test that passes at random.
		why := ""
		satisfied := false
		for _, have := range candidates {
			v, reason := contract.Satisfies(impl.Language, req, contract.SignatureOf(have))
			if v != contract.Mismatch {
				satisfied = true
				break
			}
			if why == "" {
				why = reason
			}
		}
		if satisfied {
			continue
		}
		out = append(out, conflictWarning(implID, impl, ifaceID, iface, req.Name,
			fmt.Sprintf("%s does not satisfy %s.%s: %s",
				impl.Name, iface.Name, req.Name, why)))
	}
	return out
}

func conflictWarning(
	implID string, impl domain.Resource,
	ifaceID string, iface domain.Resource,
	method, message string,
) domain.TopologyWarning {
	return domain.TopologyWarning{
		// One warning per (implementer, interface, method), so fixing one method retires
		// exactly its own row.
		ID:       implID + "@" + string(domain.WarnInterfaceConflict) + "@" + ifaceID + "::" + method,
		SourceID: ifaceID,
		TargetID: implID,
		Kind:     domain.WarnInterfaceConflict,
		Message:  message,
	}
}

// requiredMethods returns what an interface demands.
//
// Two shapes, because the languages publish it two ways. Rust, TypeScript and Java give an
// interface node its own method list. Python has no interface node at all -- an abstract
// base class is an ordinary class -- so its requirements are its own methods carrying the
// abstractmethod decorator.
func requiredMethods(topo *domain.Topology, iface domain.Resource) []contract.Signature {
	if sigs := contract.InterfaceMethods(iface); len(sigs) > 0 {
		return sigs
	}
	var out []contract.Signature
	for id := range methodIDs(iface) {
		m, ok := topo.Resources[id]
		if !ok || !isAbstractMethod(m) {
			continue
		}
		out = append(out, contract.SignatureOf(m))
	}
	return out
}

// providedMethods indexes a type's own methods by name, keeping every overload. Collapsing
// them would make the verdict depend on which one a map happened to yield.
func providedMethods(topo *domain.Topology, impl domain.Resource) map[string][]domain.Resource {
	out := map[string][]domain.Resource{}
	for id := range methodIDs(impl) {
		if m, ok := topo.Resources[id]; ok && m.Name != "" {
			out[m.Name] = append(out[m.Name], m)
		}
	}
	return out
}

// concreteOnly drops abstract declarations: re-declaring a method abstract is not
// implementing it.
func concreteOnly(methods []domain.Resource) []domain.Resource {
	out := methods[:0]
	for _, m := range methods {
		if !isAbstractMethod(m) {
			out = append(out, m)
		}
	}
	return out
}

// isDerived reports whether a struct gets this trait from a derive macro.
func isDerived(impl domain.Resource, traitName string) bool {
	raw, ok := impl.Properties["derives"]
	if !ok || traitName == "" {
		return false
	}
	switch list := raw.(type) {
	case []any:
		for _, d := range list {
			if s, ok := d.(string); ok && s == traitName {
				return true
			}
		}
	case []string:
		for _, d := range list {
			if d == traitName {
				return true
			}
		}
	}
	return false
}

// methodIDs collects the ids of a type's methods across the edge names the languages use.
func methodIDs(res domain.Resource) map[string]bool {
	out := map[string]bool{}
	for _, kind := range []string{"methods", "has_method", "has_function"} {
		for _, id := range res.Connections[kind] {
			out[id] = true
		}
	}
	return out
}

// inheritedMethod reports whether a superclass CONCRETELY provides the name.
//
// Abstract methods are skipped, and that distinction is the whole point: the abstract
// declaration is the requirement, so counting it as its own fulfilment makes every unmet
// requirement look met. A concrete method on an intermediate base does satisfy a
// grandparent's abstract one, which is why the walk continues past it rather than stopping
// at the first parent.
//
// Bounded because an inheritance cycle in a half-parsed file must not hang a scan.
func inheritedMethod(topo *domain.Topology, impl domain.Resource, name string, depth int) bool {
	if depth > 8 {
		return false
	}
	for _, parentID := range impl.Connections["inherits"] {
		parent, ok := topo.Resources[parentID]
		if !ok {
			continue
		}
		if len(concreteOnly(providedMethods(topo, parent)[name])) > 0 {
			return true
		}
		if inheritedMethod(topo, parent, name, depth+1) {
			return true
		}
	}
	return false
}

// declaresAbstract reports whether a type declares any abstract method of its own.
func declaresAbstract(methods map[string][]domain.Resource) bool {
	for _, group := range methods {
		for _, m := range group {
			if isAbstractMethod(m) {
				return true
			}
		}
	}
	return false
}

// isAbstractMethod reports whether a Python method is declared abstract.
func isAbstractMethod(m domain.Resource) bool {
	raw, ok := m.Properties["decorators"]
	if !ok {
		return false
	}
	list, ok := raw.([]any)
	if !ok {
		if strs, ok := raw.([]string); ok {
			for _, d := range strs {
				if isAbstractDecorator(d) {
					return true
				}
			}
		}
		return false
	}
	for _, d := range list {
		if s, ok := d.(string); ok && isAbstractDecorator(s) {
			return true
		}
	}
	return false
}

func isAbstractDecorator(d string) bool {
	switch d {
	case "abstractmethod", "abc.abstractmethod",
		"abstractproperty", "abc.abstractproperty",
		"abstractclassmethod", "abstractstaticmethod":
		return true
	}
	return false
}
