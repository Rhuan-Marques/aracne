package javascanner

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	java "github.com/Rhuan-Marques/aracne/internal/topology/java"
)

// ---- fixtures -------------------------------------------------------------
//
// A tiny Maven-ish corpus, package-rooted under com.t.* so every resource ID is
// a pure FQN (com.t.shapes.Circle, com.t.shapes.Circle.area(int), ...). The
// groupId in the pom is intentionally unrelated to com.t so the build-file dep
// scan cannot pollute internal type resolution.

const pomXML = `<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>org.example</groupId>
  <artifactId>unit</artifactId>
  <version>1.0.0</version>
</project>
`

const srcShape = `package com.t.shapes;
public interface Shape {
    double area();
}
`

// Circle: a class with a field, a constructor (Circle.<init>(double)), and an
// OVERLOAD area(int) (distinct ID from area()) that calls the no-arg sibling.
const srcCircle = `package com.t.shapes;
public class Circle implements Shape {
    private final double radius;
    public Circle(double radius) {
        this.radius = radius;
    }
    public double area() {
        return 3.14 * radius * radius;
    }
    public double area(int scale) {
        return area() * scale;
    }
}
`

const srcRectangle = `package com.t.shapes;
public class Rectangle implements Shape {
    private final double w;
    private final double h;
    public Rectangle(double w, double h) {
        this.w = w;
        this.h = h;
    }
    public double area() {
        return w * h;
    }
}
`

// Factory: static methods whose declared return type is the Shape interface but
// which construct concrete types (constructor + uses_struct edges, imports_module
// to the shapes files).
const srcFactory = `package com.t.factory;
import com.t.shapes.Circle;
import com.t.shapes.Rectangle;
import com.t.shapes.Shape;
public final class Factory {
    public static Shape makeCircle(double r) {
        return new Circle(r);
    }
    public static Shape makeRect(double w, double h) {
        return new Rectangle(w, h);
    }
}
`

// Consumer: the cross-file flagship. Receives a Factory return into a Shape-typed
// local and calls .area() on it -> calls Factory.make*, calls Shape.area()
// resolved through the factory's return type, uses_struct Factory,
// uses_interface Shape, imports_module factory + shapes.
const srcConsumer = `package com.t.consumer;
import com.t.factory.Factory;
import com.t.shapes.Shape;
public class Consumer {
    public double total() {
        Shape s = Factory.makeCircle(2.0);
        double a = s.area();
        Shape r = Factory.makeRect(2.0, 3.0);
        return a + r.area();
    }
}
`

// Inheritance: an abstract base + concrete subclass (struct<->struct inherits),
// and an interface extending two interfaces (iface<->iface inherits).
const srcBase = `package com.t.inh;
public abstract class Base {
    public abstract int rank();
    public String label() {
        return "b:" + rank();
    }
}
`

const srcDerived = `package com.t.inh;
public class Derived extends Base {
    public int rank() {
        return 1;
    }
}
`

const srcNamed = `package com.t.inh;
public interface Named {
    String name();
}
`

const srcSized = `package com.t.inh;
public interface Sized {
    int size();
}
`

const srcDescribable = `package com.t.inh;
public interface Describable extends Named, Sized {
    String describe();
}
`

const srcOperation = `package com.t.enums;
public interface Operation {
    int apply(int a, int b);
}
`

// Op: enum implementing an interface, with a constant body (PLUS) that becomes
// its own struct (Op$PLUS) inheriting the enum.
const srcOp = `package com.t.enums;
public enum Op implements Operation {
    PLUS {
        public int apply(int a, int b) {
            return a + b;
        }
    },
    MINUS;
    public int apply(int a, int b) {
        return a - b;
    }
    public String symbol() {
        return "op";
    }
}
`

const srcLocated = `package com.t.records;
public interface Located {
    int manhattan();
}
`

// Point: a record (Components x,y synthesize accessors x()/y()) with a compact
// constructor and an implemented interface.
const srcPoint = `package com.t.records;
public record Point(int x, int y) implements Located {
    public Point {
        if (x < 0) {
            throw new IllegalArgumentException("neg");
        }
    }
    public int manhattan() {
        return Math.abs(x) + Math.abs(y);
    }
}
`

// Outer: every nested-type flavour -> static nested (Outer.Nested), inner
// (Outer.Inner), local class in a method (Outer$Helper), anonymous class
// (Outer$anon1).
const srcOuter = `package com.t.nested;
public class Outer {
    private int Inner;
    private final String tag = "outer";
    public static class Nested {
        public int twice(int x) {
            return x * 2;
        }
    }
    public class Inner {
        public String describe() {
            return tag;
        }
    }
    public int compute(int seed) {
        class Helper {
            int boost() {
                return seed + 1;
            }
        }
        Helper helper = new Helper();
        return helper.boost();
    }
    public Runnable task() {
        return new Runnable() {
            public void run() {
                System.out.println(tag);
            }
        };
    }
}
`

// Settings: a static initializer block (<clinit>) and an instance initializer
// block (<instance-init>), each calling a helper.
const srcSettings = `package com.t.config;
public class Settings {
    private static final String GLOBAL;
    private final String local;
    static {
        GLOBAL = defaultGlobal();
    }
    {
        local = defaultLocal();
    }
    public Settings() {
    }
    private static String defaultGlobal() {
        return "global";
    }
    private String defaultLocal() {
        return "local";
    }
}
`

// Marker: an annotation type (@interface) -> interface with IsAnnotation.
const srcMarker = `package com.t.ann;
public @interface Marker {
    String value();
    int priority() default 0;
}
`

// ExternalUser: imports only the JDK (java.util) -> imports_dependency java.util,
// no internal edges.
const srcExternalUser = `package com.t.ext;
import java.util.ArrayList;
import java.util.List;
public class ExternalUser {
    public List<String> names() {
        List<String> out = new ArrayList<>();
        out.add("a");
        return out;
    }
}
`

// Box: signature-normalization probes -> join(String[]) from varargs,
// total(List) from List<String>, sum(int[]) keeps array dims.
const srcBox = `package com.t.gen;
import java.util.List;
public class Box {
    public String join(String... parts) {
        return String.join(",", parts);
    }
    public double total(List<String> xs) {
        return xs.size();
    }
    public int sum(int[] xs) {
        return xs.length;
    }
}
`

// shapesFiles returns the three shapes sources keyed by their Maven path.
func shapesFiles() map[string]string {
	return map[string]string{
		"src/main/java/com/t/shapes/Shape.java":     srcShape,
		"src/main/java/com/t/shapes/Circle.java":    srcCircle,
		"src/main/java/com/t/shapes/Rectangle.java": srcRectangle,
	}
}

// crossFileFiles is shapes + factory + consumer + inheritance + enums: a corpus
// rich enough to exercise every cross-file edge and to use for determinism.
func crossFileFiles() map[string]string {
	m := shapesFiles()
	m["src/main/java/com/t/factory/Factory.java"] = srcFactory
	m["src/main/java/com/t/consumer/Consumer.java"] = srcConsumer
	m["src/main/java/com/t/inh/Base.java"] = srcBase
	m["src/main/java/com/t/inh/Derived.java"] = srcDerived
	m["src/main/java/com/t/inh/Named.java"] = srcNamed
	m["src/main/java/com/t/inh/Sized.java"] = srcSized
	m["src/main/java/com/t/inh/Describable.java"] = srcDescribable
	m["src/main/java/com/t/enums/Operation.java"] = srcOperation
	m["src/main/java/com/t/enums/Op.java"] = srcOp
	return m
}

// extractionFiles covers the single-file resource-kind surface (nested, enum,
// record, annotation, init blocks) plus the shapes anchor.
func extractionFiles() map[string]string {
	m := shapesFiles()
	m["src/main/java/com/t/enums/Operation.java"] = srcOperation
	m["src/main/java/com/t/enums/Op.java"] = srcOp
	m["src/main/java/com/t/records/Located.java"] = srcLocated
	m["src/main/java/com/t/records/Point.java"] = srcPoint
	m["src/main/java/com/t/nested/Outer.java"] = srcOuter
	m["src/main/java/com/t/config/Settings.java"] = srcSettings
	m["src/main/java/com/t/ann/Marker.java"] = srcMarker
	return m
}

// ---- helpers --------------------------------------------------------------

// writeProj writes a pom.xml plus the given files (keys are paths relative to the
// project root) into a fresh temp dir and returns the project root.
func writeProj(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pom.xml"), []byte(pomXML), 0o644); err != nil {
		t.Fatal(err)
	}
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// jScan scans a directory with a fresh JavaScanner.
func jScan(t *testing.T, dir string) *domain.Topology {
	t.Helper()
	topo, err := NewJavaScanner().Scan(dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return topo
}

// mustRes returns the resource with the EXACT id, failing if it is absent.
func mustRes(t *testing.T, topo *domain.Topology, id string) domain.Resource {
	t.Helper()
	r, ok := topo.Resources[id]
	if !ok {
		t.Fatalf("resource %q not found", id)
	}
	return r
}

// hasID reports whether a resource with the EXACT id exists.
func hasID(topo *domain.Topology, id string) bool {
	_, ok := topo.Resources[id]
	return ok
}

// connHas reports whether the resource id has a conn edge to the EXACT target.
func connHas(topo *domain.Topology, id, conn, target string) bool {
	r, ok := topo.Resources[id]
	if !ok {
		return false
	}
	for _, tgt := range r.Connections[conn] {
		if tgt == target {
			return true
		}
	}
	return false
}

// connHasSuffix reports whether the resource id has a conn edge to a target whose
// ID ends with targetSuffix (used for file/module edges keyed by absolute path).
func connHasSuffix(topo *domain.Topology, id, conn, targetSuffix string) bool {
	r, ok := topo.Resources[id]
	if !ok {
		return false
	}
	for _, tgt := range r.Connections[conn] {
		if strings.HasSuffix(tgt, targetSuffix) {
			return true
		}
	}
	return false
}

// idEndingWith returns the unique resource ID ending with suffix (used for file
// nodes, whose IDs are absolute paths).
func idEndingWith(t *testing.T, topo *domain.Topology, suffix string) string {
	t.Helper()
	var found []string
	for id := range topo.Resources {
		if strings.HasSuffix(id, suffix) {
			found = append(found, id)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly one ID ending with %q, got %v", suffix, found)
	}
	return found[0]
}

func boolPropOf(r domain.Resource, key string) bool {
	if v, ok := r.Properties[key]; ok && v != nil {
		b, _ := v.(bool)
		return b
	}
	return false
}

// ---- tests ----------------------------------------------------------------

func TestScannerMetadata(t *testing.T) {
	s := NewJavaScanner()
	if s.Name() != "java" {
		t.Errorf("Name() = %q, want java", s.Name())
	}
	exts := s.Extensions()
	if len(exts) != 1 || exts[0] != ".java" {
		t.Errorf("Extensions() = %v, want [.java]", exts)
	}
}

func TestDetect(t *testing.T) {
	// pom.xml present -> detected.
	withPom := writeProj(t, map[string]string{
		"src/main/java/com/t/shapes/Shape.java": srcShape,
	})
	if !NewJavaScanner().Detect(withPom) {
		t.Error("Detect should be true for a project with pom.xml")
	}

	// Bare *.java at the root, no build file -> detected.
	bareJava := t.TempDir()
	if err := os.WriteFile(filepath.Join(bareJava, "Main.java"), []byte("public class Main {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !NewJavaScanner().Detect(bareJava) {
		t.Error("Detect should be true for a bare .java file")
	}

	// Empty dir -> not detected.
	if NewJavaScanner().Detect(t.TempDir()) {
		t.Error("Detect should be false for an empty dir")
	}
}

// collectJavaFiles must skip build-output dirs (target/build/.gradle/out/bin),
// test/tests dirs, and *Test.java/*Tests.java/*IT.java by name.
func TestCollectJavaFilesSkips(t *testing.T) {
	dir := writeProj(t, map[string]string{
		"src/main/java/com/t/Keep.java":      "package com.t;\npublic class Keep {}\n",
		"target/InTarget.java":               "package com.t;\npublic class InTarget {}\n",
		"build/InBuild.java":                 "package com.t;\npublic class InBuild {}\n",
		".gradle/InGradle.java":              "package com.t;\npublic class InGradle {}\n",
		"out/InOut.java":                     "package com.t;\npublic class InOut {}\n",
		"bin/InBin.java":                     "package com.t;\npublic class InBin {}\n",
		"src/test/java/com/t/InTestDir.java": "package com.t;\npublic class InTestDir {}\n",
		"tests/InTestsDir.java":              "package com.t;\npublic class InTestsDir {}\n",
		"src/main/java/com/t/FooTest.java":   "package com.t;\npublic class FooTest {}\n",
		"src/main/java/com/t/BarTests.java":  "package com.t;\npublic class BarTests {}\n",
		"src/main/java/com/t/BazIT.java":     "package com.t;\npublic class BazIT {}\n",
	})

	files := collectJavaFiles(dir)
	if len(files) != 1 || !strings.HasSuffix(files[0], "Keep.java") {
		t.Fatalf("collectJavaFiles = %v, want only Keep.java", files)
	}

	topo := jScan(t, dir)
	if !hasID(topo, "com.t.Keep") {
		t.Error("com.t.Keep should be indexed")
	}
	for _, gone := range []string{
		"com.t.InTarget", "com.t.InBuild", "com.t.InGradle", "com.t.InOut",
		"com.t.InBin", "com.t.InTestDir", "com.t.InTestsDir",
		"com.t.FooTest", "com.t.BarTests", "com.t.BazIT",
	} {
		if hasID(topo, gone) {
			t.Errorf("%s should have been skipped", gone)
		}
	}
}

// TestResourceExtraction locks the exact resource IDs and kinds for every Java
// resource flavour.
func TestResourceExtraction(t *testing.T) {
	topo := jScan(t, writeProj(t, extractionFiles()))

	// Class FQN, interface, method-with-signature, overload distinctness, ctor.
	if k := mustRes(t, topo, "com.t.shapes.Circle").Kind; k != domain.ResourceStruct {
		t.Errorf("Circle kind = %v, want struct", k)
	}
	if k := mustRes(t, topo, "com.t.shapes.Shape").Kind; k != domain.ResourceInterface {
		t.Errorf("Shape kind = %v, want interface", k)
	}
	for _, id := range []string{
		"com.t.shapes.Circle.area()",
		"com.t.shapes.Circle.area(int)",      // overload: distinct ID from area()
		"com.t.shapes.Circle.<init>(double)", // constructor
	} {
		if k := mustRes(t, topo, id).Kind; k != domain.ResourceMethod {
			t.Errorf("%s kind = %v, want method", id, k)
		}
	}
	if !boolPropOf(mustRes(t, topo, "com.t.shapes.Circle.<init>(double)"), "is_constructor") {
		t.Error("Circle.<init>(double) should have is_constructor=true")
	}
	// area(int) calls the no-arg sibling (intra-class call edge).
	if !connHas(topo, "com.t.shapes.Circle.area(int)", "calls", "com.t.shapes.Circle.area()") {
		t.Error("area(int) should call area()")
	}

	// Nested dotted FQNs + local ($Helper) + anonymous ($anon1).
	for id, kind := range map[string]domain.ResourceKind{
		"com.t.nested.Outer":        domain.ResourceStruct,
		"com.t.nested.Outer.Nested": domain.ResourceStruct, // static nested, dotted
		"com.t.nested.Outer.Inner":  domain.ResourceStruct, // inner, dotted
		"com.t.nested.Outer$Helper": domain.ResourceStruct, // local class
		"com.t.nested.Outer$anon1":  domain.ResourceStruct, // anonymous class
	} {
		if k := mustRes(t, topo, id).Kind; k != kind {
			t.Errorf("%s kind = %v, want %v", id, k, kind)
		}
	}
	if !boolPropOf(mustRes(t, topo, "com.t.nested.Outer$Helper"), "is_local") {
		t.Error("Outer$Helper should have is_local=true")
	}
	if !boolPropOf(mustRes(t, topo, "com.t.nested.Outer$anon1"), "is_anonymous") {
		t.Error("Outer$anon1 should have is_anonymous=true")
	}
	if !hasID(topo, "com.t.nested.Outer$anon1.run()") {
		t.Error("anonymous class method run() should exist")
	}
	// The int field Inner and the inner class Inner are distinct nodes.
	if !hasID(topo, "com.t.nested.Outer.Inner") {
		t.Error("inner class Outer.Inner should be its own node")
	}

	// <clinit> / <instance-init>, both synthetic, each calling its helper.
	clinit := mustRes(t, topo, "com.t.config.Settings.<clinit>()")
	if !boolPropOf(clinit, "is_synthetic") {
		t.Error("<clinit> should be synthetic")
	}
	if !connHas(topo, "com.t.config.Settings.<clinit>()", "calls", "com.t.config.Settings.defaultGlobal()") {
		t.Error("<clinit> should call defaultGlobal()")
	}
	if !boolPropOf(mustRes(t, topo, "com.t.config.Settings.<instance-init>()"), "is_synthetic") {
		t.Error("<instance-init> should be synthetic")
	}
	if !connHas(topo, "com.t.config.Settings.<instance-init>()", "calls", "com.t.config.Settings.defaultLocal()") {
		t.Error("<instance-init> should call defaultLocal()")
	}

	// Enum + variants (+ constant-body struct).
	jt := java.FromGeneric(topo)
	op := jt.Classes["com.t.enums.Op"]
	if !op.IsEnum {
		t.Error("Op should have IsEnum=true")
	}
	if got := append([]string(nil), op.Variants...); !contains(got, "PLUS") || !contains(got, "MINUS") {
		t.Errorf("Op.Variants = %v, want PLUS and MINUS", got)
	}
	if k := mustRes(t, topo, "com.t.enums.Op$PLUS").Kind; k != domain.ResourceStruct {
		t.Errorf("Op$PLUS kind = %v, want struct", k)
	}
	if !connHas(topo, "com.t.enums.Op$PLUS", "inherits", "com.t.enums.Op") {
		t.Error("Op$PLUS should inherit Op")
	}

	// Record + components + synthesized accessor x().
	pt := jt.Classes["com.t.records.Point"]
	if !pt.IsRecord {
		t.Error("Point should have IsRecord=true")
	}
	if len(pt.Components) != 2 || pt.Components[0].Name != "x" || pt.Components[1].Name != "y" {
		t.Errorf("Point.Components = %+v, want [x y]", pt.Components)
	}
	acc := mustRes(t, topo, "com.t.records.Point.x()")
	if !boolPropOf(acc, "is_synthetic") {
		t.Error("record accessor x() should be synthetic")
	}
	if !hasID(topo, "com.t.records.Point.y()") {
		t.Error("record accessor y() should exist")
	}

	// @interface annotation type.
	mk := jt.Interfaces["com.t.ann.Marker"]
	if !mk.IsAnnotation {
		t.Error("Marker should have IsAnnotation=true")
	}
	if k := mustRes(t, topo, "com.t.ann.Marker").Kind; k != domain.ResourceInterface {
		t.Errorf("Marker kind = %v, want interface", k)
	}
}

// TestCrossFileResolution locks the cross-file edges: implements/implemented_by,
// class & interface inheritance, factory-return method resolution, imports_module.
func TestCrossFileResolution(t *testing.T) {
	topo := jScan(t, writeProj(t, crossFileFiles()))

	// implements / implemented_by, both directions, cross-file.
	if !connHas(topo, "com.t.shapes.Circle", "implements", "com.t.shapes.Shape") {
		t.Error("Circle should implement Shape")
	}
	if !connHas(topo, "com.t.shapes.Shape", "implemented_by", "com.t.shapes.Circle") {
		t.Error("Shape should be implemented_by Circle")
	}
	if !connHas(topo, "com.t.shapes.Shape", "implemented_by", "com.t.shapes.Rectangle") {
		t.Error("Shape should be implemented_by Rectangle")
	}

	// class extends class -> struct<->struct inherits.
	if !connHas(topo, "com.t.inh.Derived", "inherits", "com.t.inh.Base") {
		t.Error("Derived should inherit Base (struct<->struct)")
	}
	if !connHas(topo, "com.t.inh.Base", "inherited_by", "com.t.inh.Derived") {
		t.Error("Base should be inherited_by Derived")
	}

	// interface extends interfaces -> iface<->iface inherits.
	if !connHas(topo, "com.t.inh.Describable", "inherits", "com.t.inh.Named") {
		t.Error("Describable should inherit Named (iface<->iface)")
	}
	if !connHas(topo, "com.t.inh.Describable", "inherits", "com.t.inh.Sized") {
		t.Error("Describable should inherit Sized (iface<->iface)")
	}
	if !connHas(topo, "com.t.inh.Named", "inherited_by", "com.t.inh.Describable") {
		t.Error("Named should be inherited_by Describable")
	}

	// enum implements interface (both directions).
	if !connHas(topo, "com.t.enums.Op", "implements", "com.t.enums.Operation") {
		t.Error("Op should implement Operation")
	}
	if !connHas(topo, "com.t.enums.Operation", "implemented_by", "com.t.enums.Op") {
		t.Error("Operation should be implemented_by Op")
	}

	// Factory constructs concrete types: constructor calls + uses_struct.
	if !connHas(topo, "com.t.factory.Factory.makeCircle(double)", "calls", "com.t.shapes.Circle.<init>(double)") {
		t.Error("makeCircle should call Circle.<init>(double)")
	}
	if !connHas(topo, "com.t.factory.Factory.makeCircle(double)", "uses_struct", "com.t.shapes.Circle") {
		t.Error("makeCircle should use_struct Circle")
	}

	// Cross-file call resolution through the factory's return type.
	total := "com.t.consumer.Consumer.total()"
	if !connHas(topo, total, "calls", "com.t.factory.Factory.makeCircle(double)") {
		t.Error("total() should call Factory.makeCircle(double)")
	}
	if !connHas(topo, total, "calls", "com.t.shapes.Shape.area()") {
		t.Error("total() should call Shape.area() (resolved through the factory return type)")
	}
	if !connHas(topo, total, "uses_struct", "com.t.factory.Factory") {
		t.Error("total() should use_struct Factory")
	}
	if !connHas(topo, total, "uses_interface", "com.t.shapes.Shape") {
		t.Error("total() should use_interface Shape")
	}

	// imports_module: file -> file (keyed by absolute path).
	consumerMod := idEndingWith(t, topo, "com/t/consumer/Consumer.java")
	if !connHasSuffix(topo, consumerMod, "imports_module", "com/t/factory/Factory.java") {
		t.Error("Consumer.java should imports_module Factory.java")
	}
	if !connHasSuffix(topo, consumerMod, "imports_module", "com/t/shapes/Shape.java") {
		t.Error("Consumer.java should imports_module Shape.java")
	}
}

// TestExternalDependency locks imports_dependency for an external import, the
// dependency node, and the absence of any false internal edge.
func TestExternalDependency(t *testing.T) {
	topo := jScan(t, writeProj(t, map[string]string{
		"src/main/java/com/t/ext/ExternalUser.java": srcExternalUser,
	}))

	if k := mustRes(t, topo, "java.util").Kind; k != domain.ResourceDependency {
		t.Errorf("java.util kind = %v, want dependency", k)
	}
	extMod := idEndingWith(t, topo, "com/t/ext/ExternalUser.java")
	if !connHas(topo, extMod, "imports_dependency", "java.util") {
		t.Error("ExternalUser.java should imports_dependency java.util")
	}
	// No false internal edge for the JDK import.
	if got := topo.Resources[extMod].Connections["imports_module"]; len(got) != 0 {
		t.Errorf("ExternalUser.java should have no imports_module edges, got %v", got)
	}
}

// TestSignatureNormalization locks the method-ID signature normalization:
// varargs T... -> T[], generic List<String> -> List, array dims kept.
func TestSignatureNormalization(t *testing.T) {
	topo := jScan(t, writeProj(t, map[string]string{
		"src/main/java/com/t/gen/Box.java": srcBox,
	}))

	if !hasID(topo, "com.t.gen.Box.join(String[])") {
		t.Error("varargs String... should normalize to String[] in the method ID")
	}
	if !hasID(topo, "com.t.gen.Box.total(List)") {
		t.Error("generic List<String> should reduce to List in the method ID")
	}
	if !hasID(topo, "com.t.gen.Box.sum(int[])") {
		t.Error("array int[] should keep its dims in the method ID")
	}
}

// TestLocalClassCollision locks the fix for same-named local classes declared in
// sibling method scopes of one enclosing type: BOTH survive with distinct IDs
// (one keeps the bare FQN, the other is disambiguated with a deterministic
// suffix), so neither is silently lost to an ID collision.
func TestLocalClassCollision(t *testing.T) {
	const src = `package com.t.loc;
public class Holder {
    public int a() {
        class Helper { int go() { return 1; } }
        return new Helper().go();
    }
    public int b() {
        class Helper { int go() { return 2; } }
        return new Helper().go();
    }
}
`
	topo := jScan(t, writeProj(t, map[string]string{
		"src/main/java/com/t/loc/Holder.java": src,
	}))
	first := "com.t.loc.Holder$Helper"
	second := "com.t.loc.Holder$Helper#2"
	// The SET of IDs is order-independent: one Helper keeps the bare FQN, the
	// other is suffixed — neither dropped.
	if !hasID(topo, first) || !hasID(topo, second) {
		t.Fatalf("both same-named local classes must survive with distinct IDs; have %q=%v %q=%v",
			first, hasID(topo, first), second, hasID(topo, second))
	}
	if !hasID(topo, first+".go()") || !hasID(topo, second+".go()") {
		t.Error("each disambiguated local class should own its own go() method node")
	}
	if !boolPropOf(mustRes(t, topo, second), "is_local") {
		t.Errorf("%s should have is_local=true", second)
	}
}

// TestDeterminism scans the same tree twice and requires identical resource sets
// and edges (order-insensitive canonical comparison).
func TestDeterminism(t *testing.T) {
	dir := writeProj(t, crossFileFiles())
	a := canonTopo(jScan(t, dir))
	b := canonTopo(jScan(t, dir))
	if a != b {
		t.Errorf("scan is not deterministic:\n--- scan A ---\n%s\n--- scan B ---\n%s", a, b)
	}
}

// canonTopo renders a topology as a stable, order-insensitive string of its
// resource kinds and edges.
func canonTopo(topo *domain.Topology) string {
	var lines []string
	for id, r := range topo.Resources {
		lines = append(lines, "R\t"+id+"\t"+string(r.Kind))
		for conn, tgts := range r.Connections {
			for _, tgt := range tgts {
				lines = append(lines, "E\t"+id+"\t"+conn+"\t"+tgt)
			}
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
