package jsscanner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aracne/internal/topology/domain"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func scanProject(t *testing.T, dir string) *domain.Topology {
	t.Helper()
	topo, err := NewJavaScriptScanner().Scan(dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return topo
}

func findByName(topo *domain.Topology, kind domain.ResourceKind, name string) *domain.Resource {
	for id := range topo.Resources {
		res := topo.Resources[id]
		if res.Kind == kind && res.Name == name {
			return &res
		}
	}
	return nil
}

func connHasSuffix(res *domain.Resource, conn, suffix string) bool {
	if res == nil {
		return false
	}
	for _, target := range res.Connections[conn] {
		if strings.HasSuffix(target, suffix) {
			return true
		}
	}
	return false
}

func TestJSScanExtractsResources(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "math.js"), `
export class Vector {
  constructor(x) { this.x = x; }
  scale(k) { return new Vector(this.x * k); }
}

export function add(a, b) { return a + b; }

export const PI = 3.14;

const arrow = (n) => n * 2;
export { arrow };
`)
	writeFile(t, filepath.Join(dir, "main.js"), `
import { add, Vector } from './math';
import _ from 'lodash';

export function compute() {
  const v = new Vector(2);
  v.scale(3);
  return add(1, 2);
}
`)

	topo := scanProject(t, dir)

	if topo.Language != "javascript" {
		t.Fatalf("expected language javascript, got %q", topo.Language)
	}

	// Functions, class, method, variable extraction.
	for _, name := range []string{"add", "arrow", "compute"} {
		if findByName(topo, domain.ResourceFunction, name) == nil {
			t.Errorf("expected function %q", name)
		}
	}
	if findByName(topo, domain.ResourceType, "Vector") == nil {
		t.Fatal("expected class Vector")
	}
	if findByName(topo, domain.ResourceMethod, "scale") == nil {
		t.Error("expected method scale")
	}
	if findByName(topo, domain.ResourceVariable, "PI") == nil {
		t.Error("expected variable PI")
	}

	// Constructor pointer on the class.
	vec := findByName(topo, domain.ResourceType, "Vector")
	if ctor, ok := vec.Properties["constructor"]; !ok || ctor == "" {
		t.Errorf("expected Vector.constructor pointer, got %v", vec.Properties["constructor"])
	}

	// Cross-module direct call: compute -> add.
	compute := findByName(topo, domain.ResourceFunction, "compute")
	if !connHasSuffix(compute, "calls", "math.add") {
		t.Errorf("expected compute to call math.add, calls=%v", compute.Connections["calls"])
	}
	// new Vector(): uses_class edge.
	if !connHasSuffix(compute, "uses_class", "math.Vector") {
		t.Errorf("expected compute to use class Vector, uses_class=%v", compute.Connections["uses_class"])
	}

	// External dependency captured on the module.
	mainMod := findByName(topo, domain.ResourceFile, "main.js")
	if !connHasSuffix(mainMod, "imports_dependency", "lodash") {
		t.Errorf("expected main.js to import lodash, imports_dependency=%v", mainMod.Connections["imports_dependency"])
	}
	if findByName(topo, domain.ResourceDependency, "lodash") == nil {
		t.Error("expected lodash dependency resource")
	}
}

func TestJSClassInheritance(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "base.js"), `
export class Base {
  greet() { return "hi"; }
}
`)
	writeFile(t, filepath.Join(dir, "child.js"), `
import { Base } from './base';
export class Child extends Base {
  extra() { return 1; }
}
`)

	topo := scanProject(t, dir)

	child := findByName(topo, domain.ResourceType, "Child")
	base := findByName(topo, domain.ResourceType, "Base")
	if child == nil || base == nil {
		t.Fatal("expected Base and Child classes")
	}
	if !connHasSuffix(child, "inherits", "base.Base") {
		t.Errorf("expected Child inherits Base, inherits=%v", child.Connections["inherits"])
	}
	if !connHasSuffix(base, "inherited_by", "child.Child") {
		t.Errorf("expected Base inherited_by Child, inherited_by=%v", base.Connections["inherited_by"])
	}
}

func TestJSCommonJSRequire(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "util.js"), `
function helper() { return 7; }
module.exports = { helper };
`)
	writeFile(t, filepath.Join(dir, "app.js"), `
const { helper } = require('./util');
const fs = require('fs');

function run() {
  return helper();
}
module.exports = run;
`)

	topo := scanProject(t, dir)

	run := findByName(topo, domain.ResourceFunction, "run")
	if run == nil {
		t.Fatal("expected function run")
	}
	if !connHasSuffix(run, "calls", "util.helper") {
		t.Errorf("expected run to call util.helper (CommonJS), calls=%v", run.Connections["calls"])
	}
	if findByName(topo, domain.ResourceDependency, "fs") == nil {
		t.Error("expected fs dependency resource")
	}
}

func TestJSParsesJSX(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Button.jsx"), `
import React from 'react';

export function Button(props) {
  return <button className="btn" onClick={props.onClick}>{props.label}</button>;
}

export class Panel extends React.Component {
  render() {
    return <div><Button label="ok" /></div>;
  }
}
`)

	topo := scanProject(t, dir)

	if len(topo.Errors) != 0 {
		t.Fatalf("expected no parse errors for JSX, got %v", topo.Errors)
	}
	if findByName(topo, domain.ResourceFunction, "Button") == nil {
		t.Error("expected JSX function component Button")
	}
	if findByName(topo, domain.ResourceType, "Panel") == nil {
		t.Error("expected JSX class component Panel")
	}
	if findByName(topo, domain.ResourceMethod, "render") == nil {
		t.Error("expected Panel.render method")
	}
}

func TestJSAsyncAndGenerator(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "io.js"), `
export async function fetchData() { return 1; }
export function* counter() { yield 1; }
`)

	topo := scanProject(t, dir)

	fd := findByName(topo, domain.ResourceFunction, "fetchData")
	if fd == nil {
		t.Fatal("expected fetchData")
	}
	if ia, _ := fd.Properties["is_async"].(bool); !ia {
		t.Errorf("expected fetchData is_async=true, props=%v", fd.Properties)
	}
	gen := findByName(topo, domain.ResourceFunction, "counter")
	if gen == nil {
		t.Fatal("expected counter")
	}
	if ig, _ := gen.Properties["is_generator"].(bool); !ig {
		t.Errorf("expected counter is_generator=true, props=%v", gen.Properties)
	}
}
