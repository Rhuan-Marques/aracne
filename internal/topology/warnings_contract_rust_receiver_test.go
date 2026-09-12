package topology_test

import "testing"

// rustReceiverCases are Rust calls whose receiver is written in a place the plain case
// never exercises: a typed `self` (async and pinned code), and a path (UFCS) call that
// passes the receiver as its first argument. Neither receiver is a declared parameter, so
// counting it made the call look wrong forever, and signature_changed never cleared (RS-8).
func rustReceiverCases() []contractLang {
	cargo := "[package]\nname = \"ctr\"\nversion = \"0.1.0\"\nedition = \"2021\"\n"
	pinned := func(params string) string {
		return "use std::pin::Pin;\npub struct Fut { pub v: i32 }\nimpl Fut {\n" +
			"    pub fn poll(self: Pin<&mut Self>" + params + ") -> i32 { 0 }\n}\n"
	}
	driver := func(args string) string {
		return "use std::pin::Pin;\nuse crate::a::Fut;\nimpl Fut {\n" +
			"    pub fn drive(self: Pin<&mut Self>) -> i32 { self.poll(" + args + ") }\n}\n"
	}
	area := func(params string) string {
		return "pub struct C { pub v: i32 }\nimpl C {\n    pub fn area(&self" + params + ") -> i32 { self.v }\n}\n"
	}
	ufcs := func(args string) string {
		return "use crate::a::C;\npub fn use_it(c: &C) -> i32 { C::area(c" + args + ") }\n"
	}
	return []contractLang{
		{
			name:   "rust typed self",
			strict: true,
			files: map[string]string{
				"Cargo.toml": cargo,
				"src/lib.rs": "pub mod a;\npub mod b;\n",
				"src/a.rs":   pinned(", x: i32"),
				"src/b.rs":   driver("1"),
			},
			calleeFile:    "src/a.rs",
			calleeOrig:    pinned(", x: i32"),
			calleeWide:    pinned(", x: i32, y: i32"),
			calleeWider:   pinned(", x: i32, y: i32, z: i32"),
			calleeNoise:   "// a comment\n" + pinned(", x: i32"),
			callerFile:    "src/b.rs",
			callerOrig:    driver("1"),
			callerFixed:   driver("1, 2"),
			callerTouched: "// touched, not fixed\n" + driver("1"),
		},
		{
			name:   "rust ufcs",
			strict: true,
			files: map[string]string{
				"Cargo.toml": cargo,
				"src/lib.rs": "pub mod a;\npub mod b;\n",
				"src/a.rs":   area(", k: i32"),
				"src/b.rs":   ufcs(", 1"),
			},
			calleeFile:    "src/a.rs",
			calleeOrig:    area(", k: i32"),
			calleeWide:    area(", k: i32, j: i32"),
			calleeWider:   area(", k: i32, j: i32, l: i32"),
			calleeNoise:   "// a comment\n" + area(", k: i32"),
			callerFile:    "src/b.rs",
			callerOrig:    ufcs(", 1"),
			callerFixed:   ufcs(", 1, 2"),
			callerTouched: "// touched, not fixed\n" + ufcs(", 1"),
		},
	}
}

// TestContractRustReceivers runs the widen / fix-the-caller / touch sequence of
// TestContractLifecyclePerLanguage and TestContractRejectsATouchThatIsNotAFix against the
// receiver forms.
func TestContractRustReceivers(t *testing.T) {
	for _, c := range rustReceiverCases() {
		t.Run(c.name, func(t *testing.T) {
			p := newContractProj(t, c)
			if n := p.callerWarnings(); n != 0 {
				t.Fatalf("a freshly scanned project must be clean, got %d warnings", n)
			}
			p.edit(c.calleeFile, c.calleeWide)
			if n := p.callerWarnings(); n == 0 {
				t.Fatal("widening the signature must warn the caller")
			}
			p.edit(c.callerFile, c.callerTouched)
			if n := p.callerWarnings(); n == 0 {
				t.Error("the call still does not fit; touching the caller must not clear it")
			}
			p.edit(c.callerFile, c.callerFixed)
			if n := p.callerWarnings(); n != 0 {
				w, _ := p.mgr.ListWarnings("", "", "")
				t.Errorf("fixing the caller must clear the warning, %d left: %+v", n, w)
			}
			// Put both back: every call fits again, and nothing is left to verify.
			p.edit(c.calleeFile, c.calleeOrig)
			p.edit(c.callerFile, c.callerOrig)
			if n := p.callerWarnings(); n != 0 {
				w, _ := p.mgr.ListWarnings("", "", "")
				t.Errorf("a project where every call fits must be clean, %d left: %+v", n, w)
			}
		})
	}
}
