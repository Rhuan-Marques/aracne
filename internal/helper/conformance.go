package helper

import (
	"fmt"

	"github.com/Rhuan-Marques/aracne/internal/topology/contract"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
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
		// A Java abstract class may leave any interface method to its subclasses -- the
		// skeletal-implementation pattern, AbstractList and its kin. It promises nothing until
		// something concrete extends it, and that subclass is held to the promise below
		// instead. TypeScript is not exempted the same way: its abstract class must still
		// declare every interface member, if only as abstract, which declaresAbstract covers.
		if impl.Language == "java" && isAbstractType(impl) {
			continue
		}
		// One warning per id: a class can reach one interface both directly and through an
		// abstract parent.
		seen := map[string]bool{}
		emit := func(ws []domain.TopologyWarning) {
			for _, w := range ws {
				if !seen[w.ID] {
					seen[w.ID] = true
					out = append(out, w)
				}
			}
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
				// TypeScript's `extends` is a SUPERCLASS, not an interface claim, and only
				// the parent's ABSTRACT members are promises the subclass has to keep.
				// Reading the parent's published method list instead reported two shapes of
				// correct code: a class that merely does not override an inherited concrete
				// method, and -- because declaration merging folds a same-name interface's
				// members into the class's list -- every subclass of a merged class. An
				// ABSTRACT subclass keeps none of it either: passing the requirement down is
				// what abstract means.
				if tsSuperclass(impl, iface, edge) {
					if isAbstractType(impl) {
						continue
					}
					emit(conflictsFor(topo, implID, impl, ifaceID, iface, nil, abstractRequirements))
					continue
				}
				emit(conflictsFor(topo, implID, impl, ifaceID, iface, nil, declaredRequirements))
			}
		}
		for _, via := range abstractAncestors(topo, impl) {
			for _, ifaceID := range via.Connections["implements"] {
				iface, ok := topo.Resources[ifaceID]
				if !ok || iface.Language != impl.Language {
					continue
				}
				emit(conflictsFor(topo, implID, impl, ifaceID, iface, &via, declaredRequirements))
			}
		}
	}
	return out
}

// requirementSource says which of a supertype's members the implementer is held to:
// everything the type publishes, or only what it declares abstract. See tsSuperclass.
type requirementSource int

const (
	declaredRequirements requirementSource = iota
	abstractRequirements
)

// conflictsFor checks one declared relationship. via is the abstract ancestor that made the
// declaration when impl inherited it rather than writing it, and nil otherwise.
func conflictsFor(
	topo *domain.Topology,
	implID string, impl domain.Resource,
	ifaceID string, iface domain.Resource,
	via *domain.Resource,
	source requirementSource,
) []domain.TopologyWarning {
	required := requiredMethods(topo, iface, source)
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
	external := hasExternalAncestor(topo, impl, 0)
	for _, req := range required {
		if req.Name == "" || req.HasDefault {
			continue // the interface supplies a body; an implementer need not
		}
		// Every Java type already has equals/hashCode/toString and the rest of Object's
		// public surface, inherited from a class no scan of the project can see. An
		// interface is free to REDECLARE them -- java.util.Comparator does exactly that --
		// and requiring them reported every implementer of such an interface, records and
		// enums included, for methods the compiler supplies.
		if impl.Language == "java" && isObjectMethod(req) {
			continue
		}
		candidates := concreteOnly(provided[req.Name])
		if len(candidates) == 0 {
			// Inheritance chains mean a superclass may satisfy it; only report when
			// nothing up the chain provides the name.
			if inheritedMethod(topo, impl, req.Name, 0) {
				continue
			}
			// `class MyList extends ArrayList<String> implements Sizeable` gets size() from
			// a superclass outside the scan. Its methods are unreadable, so ANY of them
			// could be the implementation and a missing-method verdict would be a guess.
			// A class with no such ancestor is still held to the promise below.
			if external {
				continue
			}
			msg := fmt.Sprintf("%s declares it implements %s but does not provide %s",
				impl.Name, iface.Name, req.Name)
			if via != nil {
				msg = fmt.Sprintf("%s extends %s, which declares it implements %s, but nothing provides %s",
					impl.Name, via.Name, iface.Name, req.Name)
			}
			out = append(out, conflictWarning(implID, impl, ifaceID, iface, req.Name, msg))
			continue
		}
		// A requirement inherited from an abstract CLASS is checked by presence only. Its
		// signature is written against the class's own type parameters -- `abstract T get()`
		// overridden by `String get()` -- and comparing that text would call the override a
		// mismatch.
		if iface.Kind != domain.ResourceInterface {
			continue
		}
		// Overloads mean one name can have several methods, and only ONE of them has to
		// fit. Java is the obvious case -- a class may declare area() and area(int), and
		// area() is the override -- but reporting the first match found would make the
		// verdict depend on map iteration order, which is a test that passes at random.
		why := ""
		satisfied := false
		typeParams := interfaceTypeParams(iface)
		for _, have := range candidates {
			v, reason := contract.SatisfiesIn(impl.Language, req, contract.SignatureOf(have),
				contract.ConformanceCtx{
					TypeParams: typeParams,
					Related: func(a, b string) bool {
						return relatedTypes(topo, a, b)
					},
				})
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

// interfaceTypeParams reads the type-parameter names an interface declares -- Java's
// `interface Repo<T>`, TypeScript's `interface Repo<T>`. Its methods are written against
// these, so a position that mentions one cannot be compared with the implementer's concrete
// type; see contract.SatisfiesWithTypeParams. Properties survive a database round trip as
// JSON, so the list arrives as []string on a fresh scan and []any when read back.
func interfaceTypeParams(iface domain.Resource) []string {
	raw, ok := iface.Properties["generics"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if name, ok := item.(string); ok && name != "" {
				out = append(out, name)
			}
		}
		return out
	}
	return nil
}

// requiredMethods returns what an interface demands.
//
// Two shapes, because the languages publish it two ways. Rust, TypeScript and Java give an
// interface node its own method list. Python has no interface node at all -- an abstract
// base class is an ordinary class -- so its requirements are its own methods carrying the
// abstractmethod decorator.
func requiredMethods(topo *domain.Topology, iface domain.Resource, source requirementSource) []contract.Signature {
	if source == declaredRequirements {
		if sigs := contract.InterfaceMethods(iface); len(sigs) > 0 {
			return sigs
		}
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

// abstractAncestors returns the Java abstract classes a type inherits from with no concrete
// class in between: the ones whose interface promises pass to it unkept. The walk stops at a
// concrete ancestor, which is checked against those promises itself, and is bounded for the
// same reason inheritedMethod is.
func abstractAncestors(topo *domain.Topology, impl domain.Resource) []domain.Resource {
	if impl.Language != "java" {
		return nil
	}
	var out []domain.Resource
	seen := map[string]bool{}
	var walk func(res domain.Resource, depth int)
	walk = func(res domain.Resource, depth int) {
		if depth > 8 {
			return
		}
		for _, parentID := range res.Connections["inherits"] {
			parent, ok := topo.Resources[parentID]
			if !ok || seen[parentID] || !isAbstractType(parent) {
				continue
			}
			seen[parentID] = true
			out = append(out, parent)
			walk(parent, depth+1)
		}
	}
	walk(impl, 0)
	return out
}

// isAbstractType reports whether a Java or TypeScript class is declared abstract.
func isAbstractType(res domain.Resource) bool {
	abstract, _ := res.Properties["is_abstract"].(bool)
	return abstract
}

// isAbstractMethod reports whether a method is declared abstract. Java and TypeScript say so
// on the method itself; Python says it with a decorator.
func isAbstractMethod(m domain.Resource) bool {
	if abstract, _ := m.Properties["is_abstract"].(bool); abstract {
		return true
	}
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

// tsSuperclass reports whether this edge is a TypeScript class extending another CLASS --
// the one conformance edge that is not a claim about an interface. See the call site.
func tsSuperclass(impl, parent domain.Resource, edge string) bool {
	return impl.Language == "typescript" && edge == "inherits" &&
		parent.Kind != domain.ResourceInterface
}

// isObjectMethod reports whether a required method is one java.lang.Object already provides,
// matched on name AND arity so an interface's own `hashCode(String salt)` stays required.
func isObjectMethod(req contract.Signature) bool {
	switch req.Name {
	case "equals":
		return len(req.Input) == 1
	case "hashCode", "toString", "clone", "finalize", "getClass", "notify", "notifyAll":
		return len(req.Input) == 0
	case "wait":
		return len(req.Input) <= 2
	}
	return false
}

// hasExternalAncestor reports whether a Java type extends a class the scan cannot see.
//
// The scanner records an `extends` clause's raw name whether or not it resolves, and the
// matcher only draws the inherits edge when the parent is a type in the project -- so a
// superclass name with no edge behind it is a class from a dependency or the JDK. Its
// methods are unreadable, and any of them may be the implementation of an interface method
// the class itself does not declare.
//
// Bounded like inheritedMethod: a cycle in a half-parsed file must not hang a scan.
func hasExternalAncestor(topo *domain.Topology, impl domain.Resource, depth int) bool {
	if impl.Language != "java" || depth > 8 {
		return false
	}
	if len(baseNames(impl)) > 0 && len(impl.Connections["inherits"]) == 0 {
		return true
	}
	for _, parentID := range impl.Connections["inherits"] {
		parent, ok := topo.Resources[parentID]
		if !ok {
			continue
		}
		if hasExternalAncestor(topo, parent, depth+1) {
			return true
		}
	}
	return false
}

// baseNames reads the superclass names a type wrote in its `extends` clause, before any
// resolution. Properties survive a database round trip as JSON, so the list arrives as
// []string on a fresh scan and []any when read back.
func baseNames(res domain.Resource) []string {
	switch v := res.Properties["bases"].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if name, ok := item.(string); ok && name != "" {
				out = append(out, name)
			}
		}
		return out
	}
	return nil
}

// relatedTypes reports whether two topology types stand in an inheritance relationship in
// either direction -- the question contract.ConformanceCtx cannot answer for itself. A
// parameter narrowed to a subtype is legal TypeScript, and so is one widened to a supertype;
// only two types with no path between them are evidence of a broken implementation.
func relatedTypes(topo *domain.Topology, a, b string) bool {
	if a == b {
		return true
	}
	return reachesType(topo, a, b, 0) || reachesType(topo, b, a, 0)
}

// reachesType walks from a type up through its supertypes looking for a target. Bounded for
// the same reason inheritedMethod is.
func reachesType(topo *domain.Topology, from, target string, depth int) bool {
	if depth > 8 {
		return false
	}
	res, ok := topo.Resources[from]
	if !ok {
		return false
	}
	for _, edge := range conformanceEdges {
		for _, parentID := range res.Connections[edge] {
			if parentID == target || reachesType(topo, parentID, target, depth+1) {
				return true
			}
		}
	}
	return false
}
