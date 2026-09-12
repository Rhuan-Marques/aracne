package topology_test

import "testing"

// An abstract class is the one implementer that may leave an interface unfinished.
//
// Java's skeletal-implementation pattern -- AbstractList and its kin -- is an abstract class
// that implements an interface partially and leaves the rest to its subclasses. Only Python's
// abstractmethod decorator used to count as "abstract", so every such class got a permanent
// warning telling the model to fix correct code. The obligation does not disappear, though:
// it passes to the first concrete subclass, which is where a missing method is a compile error.

func TestConformanceAbstractImplementers(t *testing.T) {
	shape := "package com.cf;\n\npublic interface Shape {\n    double area();\n    String name();\n}\n"
	abstractShape := "package com.cf;\n\npublic abstract class AbstractShape implements Shape {\n" +
		"    public String name() { return \"abs\"; }\n}\n"
	runConformanceCases(t, []conformanceCase{
		{
			name: "java: an abstract class leaves an interface method to its subclass",
			files: javaProj(map[string]string{
				"Shape.java":         shape,
				"AbstractShape.java": abstractShape,
				"Square.java": "package com.cf;\n\npublic class Square extends AbstractShape {\n" +
					"    public double area() { return 4.0; }\n}\n",
			}),
			want: 0,
		},
		{
			name: "java: the concrete subclass that still misses it is reported",
			files: javaProj(map[string]string{
				"Shape.java":         shape,
				"AbstractShape.java": abstractShape,
				"Square.java": "package com.cf;\n\npublic class Square extends AbstractShape {\n" +
					"    public double side() { return 4.0; }\n}\n",
			}),
			want:    1,
			message: "nothing provides area",
		},
		{
			name: "java: a concrete subclass must implement its parent's abstract method",
			files: javaProj(map[string]string{
				"Base.java": "package com.cf;\n\npublic abstract class Base {\n" +
					"    public abstract int rank();\n    public int twice() { return rank() * 2; }\n}\n",
				"Kid.java": "package com.cf;\n\npublic class Kid extends Base {\n" +
					"    public int other() { return 1; }\n}\n",
			}),
			want:    1,
			message: "rank",
		},
		{
			name: "java: an override written against the parent's type parameter",
			files: javaProj(map[string]string{
				"Repo.java": "package com.cf;\n\npublic abstract class Repo<T> {\n    public abstract T find();\n}\n",
				"Users.java": "package com.cf;\n\npublic class Users extends Repo<String> {\n" +
					"    public String find() { return \"u\"; }\n}\n",
			}),
			want: 0,
		},
		{
			// TypeScript is not exempted like Java: its abstract class must still declare every
			// interface member, if only as abstract.
			name: "typescript: an abstract class that omits an interface member is still reported",
			files: tsProj(map[string]string{
				"a.ts": "export interface Shape { area(): number; name(): string; }\n" +
					"export abstract class Base implements Shape { name(): string { return 'b'; } }\n",
			}),
			want:    1,
			message: "area",
		},
		{
			name: "typescript: a member declared abstract passes to the concrete subclass",
			files: tsProj(map[string]string{
				"a.ts": "export interface Shape { area(): number; }\n" +
					"export abstract class Base implements Shape { abstract area(): number; }\n" +
					"export class Circle extends Base { }\n",
			}),
			want:    1,
			message: "area",
		},
		{
			name: "typescript: the concrete subclass that implements it is clean",
			files: tsProj(map[string]string{
				"a.ts": "export interface Shape { area(): number; }\n" +
					"export abstract class Base implements Shape { abstract area(): number; }\n" +
					"export class Circle extends Base { area(): number { return 1; } }\n",
			}),
			want: 0,
		},
	})
}
