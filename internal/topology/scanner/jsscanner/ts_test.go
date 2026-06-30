package jsscanner

import (
	"path/filepath"
	"testing"

	"aracne/internal/topology/domain"
)

func scanTS(t *testing.T, dir string) *domain.Topology {
	t.Helper()
	topo, err := NewTypeScriptScanner().Scan(dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return topo
}

func TestTSInterfacesAndImplements(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "svc.ts"), `
export interface Service {
  run(x: number): string;
}
export interface Logger extends Service {
  log(msg: string): void;
}
export class RealService implements Service {
  run(x: number): string { return String(x); }
}
`)
	topo := scanTS(t, dir)

	if topo.Language != "typescript" {
		t.Fatalf("expected language typescript, got %q", topo.Language)
	}

	svc := findByName(topo, domain.ResourceInterface, "Service")
	if svc == nil {
		t.Fatal("expected interface Service")
	}
	cls := findByName(topo, domain.ResourceStruct, "RealService")
	if cls == nil {
		t.Fatal("expected class RealService")
	}
	if !connHasSuffix(cls, "implements", "svc.Service") {
		t.Errorf("expected RealService implements Service, implements=%v", cls.Connections["implements"])
	}
	if !connHasSuffix(svc, "implemented_by", "svc.RealService") {
		t.Errorf("expected Service implemented_by RealService, implemented_by=%v", svc.Connections["implemented_by"])
	}

	logger := findByName(topo, domain.ResourceInterface, "Logger")
	if logger == nil {
		t.Fatal("expected interface Logger")
	}
	if !connHasSuffix(logger, "inherits", "svc.Service") {
		t.Errorf("expected Logger extends Service, inherits=%v", logger.Connections["inherits"])
	}
}

func TestTSTypeAliasAndEnum(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "types.ts"), `
export type ID = string | number;
export enum Color { Red, Green = 2, Blue }
`)
	topo := scanTS(t, dir)

	id := findByName(topo, domain.ResourceNamedType, "ID")
	if id == nil {
		t.Fatal("expected named type ID")
	}
	color := findByName(topo, domain.ResourceNamedType, "Color")
	if color == nil {
		t.Fatal("expected enum Color as a named type")
	}
	if k, _ := color.Properties["kind"].(string); k != "enum" {
		t.Errorf("expected Color kind=enum, got %v", color.Properties["kind"])
	}
	members, _ := color.Properties["members"].([]string)
	if len(members) != 3 {
		t.Errorf("expected 3 enum members, got %v", members)
	}
}

func TestTSAnnotationDrivenResolution(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "app.ts"), `
export class Svc {
  run(): number { return 1; }
}

export function use(s: Svc): number {
  return s.run();
}

export interface Greeter { hi(): string; }

export function greet(g: Greeter): string {
  return g.hi();
}
`)
	topo := scanTS(t, dir)

	use := findByName(topo, domain.ResourceFunction, "use")
	if use == nil {
		t.Fatal("expected function use")
	}
	// Typed param `s: Svc` must resolve the method call with NO `new` (the headline TS win).
	if !connHasSuffix(use, "uses_class", "app.Svc") {
		t.Errorf("expected use uses_class Svc, uses_class=%v", use.Connections["uses_class"])
	}
	if !connHasSuffix(use, "calls", "app.Svc.run") {
		t.Errorf("expected use to call Svc.run via annotation, calls=%v", use.Connections["calls"])
	}

	// Interface-typed param records interface usage.
	greet := findByName(topo, domain.ResourceFunction, "greet")
	if !connHasSuffix(greet, "uses_interface", "app.Greeter") {
		t.Errorf("expected greet uses_interface Greeter, uses_interface=%v", greet.Connections["uses_interface"])
	}
}

func TestTSXParsesWithTypes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Button.tsx"), `
import React from 'react';

interface Props { label: string; }

export function Button(props: Props) {
  return <button className="b">{props.label}</button>;
}

export class Panel extends React.Component {
  render() { return <div><Button label="ok" /></div>; }
}
`)
	topo := scanTS(t, dir)

	if len(topo.Errors) != 0 {
		t.Fatalf("expected no parse errors for TSX, got %v", topo.Errors)
	}
	if findByName(topo, domain.ResourceFunction, "Button") == nil {
		t.Error("expected TSX function component Button")
	}
	if findByName(topo, domain.ResourceInterface, "Props") == nil {
		t.Error("expected interface Props")
	}
	if findByName(topo, domain.ResourceMethod, "render") == nil {
		t.Error("expected Panel.render method")
	}
	btn := findByName(topo, domain.ResourceFunction, "Button")
	if !connHasSuffix(btn, "uses_interface", "Button.Props") {
		t.Errorf("expected Button uses_interface Props, uses_interface=%v", btn.Connections["uses_interface"])
	}
}

func TestTSDeclarationFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ambient.d.ts"), `
export interface Config { name: string; }
declare function setup(c: Config): void;
`)
	topo := scanTS(t, dir)

	if len(topo.Errors) != 0 {
		t.Fatalf("expected no parse errors for .d.ts, got %v", topo.Errors)
	}
	if findByName(topo, domain.ResourceInterface, "Config") == nil {
		t.Error("expected interface Config from .d.ts")
	}
	if findByName(topo, domain.ResourceFunction, "setup") == nil {
		t.Error("expected ambient function setup from .d.ts")
	}
}

func TestTSCrossModuleTypeResolution(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "service.ts"), `
export interface Service { run(): string; }
export class Impl { go(): number { return 1; } }
`)
	writeFile(t, filepath.Join(dir, "app.ts"), `
import { Service, Impl } from './service';

export function dispatch(s: Service): string {
  return s.run();
}

export function build(): void {
  const i = new Impl();
  i.go();
}
`)
	topo := scanTS(t, dir)

	dispatch := findByName(topo, domain.ResourceFunction, "dispatch")
	if !connHasSuffix(dispatch, "uses_interface", "service.Service") {
		t.Errorf("expected dispatch uses_interface Service (imported), uses_interface=%v", dispatch.Connections["uses_interface"])
	}

	build := findByName(topo, domain.ResourceFunction, "build")
	if !connHasSuffix(build, "uses_class", "service.Impl") {
		t.Errorf("expected build uses_class Impl (imported), uses_class=%v", build.Connections["uses_class"])
	}
	if !connHasSuffix(build, "calls", "service.Impl.go") {
		t.Errorf("expected build to call Impl.go (imported), calls=%v", build.Connections["calls"])
	}
}

func TestTSAbstractClass(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "base.ts"), `
export abstract class Base {
  abstract handle(): void;
  shared(): number { return 1; }
}
`)
	topo := scanTS(t, dir)

	base := findByName(topo, domain.ResourceStruct, "Base")
	if base == nil {
		t.Fatal("expected abstract class Base")
	}
	if ab, _ := base.Properties["is_abstract"].(bool); !ab {
		t.Errorf("expected Base is_abstract=true, props=%v", base.Properties)
	}
	if findByName(topo, domain.ResourceMethod, "handle") == nil {
		t.Error("expected abstract method handle")
	}
	if findByName(topo, domain.ResourceMethod, "shared") == nil {
		t.Error("expected method shared")
	}
}
