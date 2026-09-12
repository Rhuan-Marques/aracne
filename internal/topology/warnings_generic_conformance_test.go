package topology_test

import "testing"

// CNF-01. An interface's methods are written against ITS type parameters; an implementer
// writes them against the type arguments it chose. Comparing those as text called every
// `class UserRepo implements Repo<User>` a mismatch ("parameter 1 of save is User but the
// interface declares T"), which is the single most common shape of generic code in Java and
// TypeScript. A type-parameter position carries no information about the concrete type, so it
// must not produce a verdict -- while everything a type parameter does NOT explain (arity, a
// concrete type in a non-generic position) must still be reported.
func TestCNF01_GenericInterfacesAreNotFalselyConflicted(t *testing.T) {
	cases := []conformanceCase{
		{
			name: "java implementer of a generic interface is clean",
			files: map[string]string{
				"src/main/java/com/x/Repo.java": `package com.x;

public interface Repo<T> {
    void save(T item);
    T find(String id);
}
`,
				"src/main/java/com/x/User.java": `package com.x;

public class User {}
`,
				"src/main/java/com/x/UserRepo.java": `package com.x;

public class UserRepo implements Repo<User> {
    public void save(User item) {}
    public User find(String id) { return new User(); }
}
`,
			},
			want: 0,
		},
		{
			name: "java arity is still checked in a generic interface",
			files: map[string]string{
				"src/main/java/com/x/Repo.java": `package com.x;

public interface Repo<T> {
    void save(T item, String tag);
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
			message: "parameter",
		},
		{
			name: "java non-generic position is still checked",
			files: map[string]string{
				"src/main/java/com/x/Repo.java": `package com.x;

public interface Repo<T> {
    void save(T item, String tag);
}
`,
				"src/main/java/com/x/User.java": `package com.x;

public class User {}
`,
				"src/main/java/com/x/UserRepo.java": `package com.x;

public class UserRepo implements Repo<User> {
    public void save(User item, int tag) {}
}
`,
			},
			want:    1,
			message: "parameter 2",
		},
		{
			name: "typescript implementer of a generic interface is clean",
			files: map[string]string{
				"tsconfig.json": `{"compilerOptions":{}}`,
				"src/repo.ts": `export class User {}

export interface Repo<T> {
  save(item: T): void;
  find(id: string): T;
}

export class UserRepo implements Repo<User> {
  save(item: User): void {}
  find(id: string): User { return new User(); }
}
`,
			},
			want: 0,
		},
		{
			name: "typescript extra required parameter is still checked",
			files: map[string]string{
				"tsconfig.json": `{"compilerOptions":{}}`,
				"src/repo.ts": `export class User {}

export interface Repo<T> {
  save(item: T): void;
}

export class UserRepo implements Repo<User> {
  save(item: User, tag: string): void {}
}
`,
			},
			want:    1,
			message: "requires",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scanConformance(t, tc.files)
			if len(got) != tc.want {
				for _, w := range got {
					t.Logf("warning: %s", w.Message)
				}
				t.Fatalf("got %d interface_conflict warning(s), want %d", len(got), tc.want)
			}
			if tc.message != "" && len(got) == 1 && !contains(got[0].Message, tc.message) {
				t.Fatalf("message %q does not mention %q", got[0].Message, tc.message)
			}
		})
	}
}
