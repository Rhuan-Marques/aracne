package topology_test

import "testing"

// CNF-02. A Java interface's `static` method is never inherited and never has to be
// implemented -- it is called on the interface itself, `Repo.empty()`. Treating it as a
// requirement put a warning on EVERY implementer the moment someone added a static factory
// to the interface. What must survive is the abstract method next to it: adding a static
// helper may not silence a genuinely missing implementation.
func TestCNF02_StaticInterfaceMethodsAreNotRequired(t *testing.T) {
	cases := []conformanceCase{
		{
			name: "static factory on an interface does not conflict its implementers",
			files: map[string]string{
				"src/main/java/com/x/Repo.java": `package com.x;

public interface Repo<T> {
    void save(T item);
    static <T> Repo<T> empty() { return null; }
    private static String tag() { return "x"; }
}
`,
				"src/main/java/com/x/User.java": `package com.x;

public class User {}
`,
				"src/main/java/com/x/UserRepo.java": `package com.x;

public class UserRepo implements Repo<User> {
    public void save(User item) {}
}
`,
			},
			want: 0,
		},
		{
			name: "a missing abstract method beside a static one is still reported",
			files: map[string]string{
				"src/main/java/com/x/Repo.java": `package com.x;

public interface Repo<T> {
    void save(T item);
    void flush();
    static <T> Repo<T> empty() { return null; }
}
`,
				"src/main/java/com/x/User.java": `package com.x;

public class User {}
`,
				"src/main/java/com/x/UserRepo.java": `package com.x;

public class UserRepo implements Repo<User> {
    public void save(User item) {}
}
`,
			},
			want:    1,
			message: "does not provide flush",
		},
	}
	runConformanceCases(t, cases)
}

// CNF-03. A Java class gets methods from places the scan cannot see: an EXTERNAL superclass
// (`extends ArrayList<String>`), and java.lang.Object, which every class extends and whose
// equals/hashCode/toString an interface may redeclare (java.util.Comparator does). Requiring
// those produced warnings on code that compiles. A class with NO unresolved ancestor that
// genuinely misses a method must still be reported.
func TestCNF03_InheritedFromOutsideTheScanIsNotRequired(t *testing.T) {
	cases := []conformanceCase{
		{
			name: "external superclass may supply the interface method",
			files: map[string]string{
				"src/main/java/com/x/Sizeable.java": `package com.x;

public interface Sizeable {
    int size();
}
`,
				"src/main/java/com/x/MyList.java": `package com.x;

import java.util.ArrayList;

public class MyList extends ArrayList<String> implements Sizeable {
}
`,
			},
			want: 0,
		},
		{
			name: "Object's own methods are never required",
			files: map[string]string{
				"src/main/java/com/x/Named.java": `package com.x;

public interface Named {
    String name();
    boolean equals(Object o);
    int hashCode();
    String toString();
}
`,
				"src/main/java/com/x/Person.java": `package com.x;

public record Person(String name, int age) implements Named {
}
`,
			},
			want: 0,
		},
		{
			name: "a class with no external ancestor is still reported",
			files: map[string]string{
				"src/main/java/com/x/Sizeable.java": `package com.x;

public interface Sizeable {
    int size();
}
`,
				"src/main/java/com/x/Empty.java": `package com.x;

public class Empty implements Sizeable {
}
`,
			},
			want:    1,
			message: "does not provide size",
		},
		{
			name: "an internal superclass that misses the method is still reported",
			files: map[string]string{
				"src/main/java/com/x/Sizeable.java": `package com.x;

public interface Sizeable {
    int size();
}
`,
				"src/main/java/com/x/Base.java": `package com.x;

public class Base {
    public String label() { return "b"; }
}
`,
				"src/main/java/com/x/Leaf.java": `package com.x;

public class Leaf extends Base implements Sizeable {
}
`,
			},
			want:    1,
			message: "does not provide size",
		},
	}
	runConformanceCases(t, cases)
}

// CNF-04. TypeScript method-syntax members are parameter-BIVARIANT even under
// strictFunctionTypes, so an implementation may narrow a parameter to a subtype. Comparing
// the type text called that a mismatch. An unrelated type in the same position is still a
// real error and must still be reported.
func TestCNF04_NarrowedParametersAreLegalTypeScript(t *testing.T) {
	cases := []conformanceCase{
		{
			name: "narrowing a parameter to a subclass is accepted",
			files: map[string]string{
				"tsconfig.json": `{"compilerOptions":{}}`,
				"src/handler.ts": `export class BaseEv {}
export class MouseEv extends BaseEv {}

export interface Handler {
  handle(e: BaseEv): void;
}

export class ClickHandler implements Handler {
  handle(e: MouseEv): void {}
}
`,
			},
			want: 0,
		},
		{
			name: "an unrelated class in the same position is still reported",
			files: map[string]string{
				"tsconfig.json": `{"compilerOptions":{}}`,
				"src/handler.ts": `export class BaseEv {}
export class Unrelated {}

export interface Handler {
  handle(e: BaseEv): void;
}

export class ClickHandler implements Handler {
  handle(e: Unrelated): void {}
}
`,
			},
			want:    1,
			message: "parameter 1",
		},
		{
			name: "a primitive in place of another primitive is still reported",
			files: map[string]string{
				"tsconfig.json": `{"compilerOptions":{}}`,
				"src/handler.ts": `export interface Handler {
  handle(e: string): void;
}

export class ClickHandler implements Handler {
  handle(e: number): void {}
}
`,
			},
			want:    1,
			message: "parameter 1",
		},
	}
	runConformanceCases(t, cases)
}

// CNF-05. TypeScript reached a class through its `inherits` edge and held it to its parent's
// members. Two shapes of correct code were reported: an ABSTRACT subclass, which may leave an
// inherited abstract member to its own subclasses, and DECLARATION MERGING, where a same-name
// interface's members are folded into the class's method list and became a requirement for
// every subclass even though the merged declaration only describes the parent itself.
func TestCNF05_TypeScriptInheritsIsNotAnInterfaceClaim(t *testing.T) {
	cases := []conformanceCase{
		{
			name: "abstract subclass need not implement an inherited abstract member",
			files: map[string]string{
				"tsconfig.json": `{"compilerOptions":{}}`,
				"src/shapes.ts": `export abstract class Base {
  abstract area(): number;
}

export abstract class Mid extends Base {
  helper(): void {}
}

export class Leaf extends Mid {
  area(): number { return 1; }
}
`,
			},
			want: 0,
		},
		{
			name: "a declaration-merged member is not required of a subclass",
			files: map[string]string{
				"tsconfig.json": `{"compilerOptions":{}}`,
				"src/merged.ts": `export class Foo {
  own(): void {}
}

export interface Foo {
  extra(): void;
}

export class Child extends Foo {
}
`,
			},
			want: 0,
		},
		{
			name: "a concrete subclass that leaves an abstract member unimplemented is still reported",
			files: map[string]string{
				"tsconfig.json": `{"compilerOptions":{}}`,
				"src/shapes.ts": `export abstract class Base {
  abstract area(): number;
}

export class Leaf extends Base {
  helper(): void {}
}
`,
			},
			want:    1,
			message: "area",
		},
		{
			name: "a class that does not deliver a declared interface is still reported",
			files: map[string]string{
				"tsconfig.json": `{"compilerOptions":{}}`,
				"src/iface.ts": `export interface Shape {
  area(): number;
}

export class Leaf implements Shape {
  helper(): void {}
}
`,
			},
			want:    1,
			message: "area",
		},
	}
	runConformanceCases(t, cases)
}
