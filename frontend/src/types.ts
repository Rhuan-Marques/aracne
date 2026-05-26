export type ResourceName = 'ExternalVar' | 'Function' | 'File' | 'Struct' | 'Package' | 'Interface' | 'Dependency'

export interface Location {
  StartsAt: number
  EndsAt: number
  Path: string
}

export interface VariableDefinition {
  Name: string
  Typing: string
}

export interface FunctionDefinition {
  Name: string
  Input: VariableDefinition[]
  Output: VariableDefinition[]
}

export interface Function {
  ID: string
  Name: string
  Description: string
  Input: VariableDefinition[]
  Output: VariableDefinition[]
  Loc: Location
  MethodFrom: string | null
  ExternalVarsUsed: string[]
  FunctionsUsed: string[]
  StructsUsed: string[]
  InterfacesUsed: string[]
  DependanciesUsed: string[]
  PackagesUsed: string[]
}

export interface Struct {
  ID: string
  Name: string
  Description: string
  Params: VariableDefinition[]
  Methods: string[]
  Constructor: string | null
  Loc: Location
  Implements: string | null
  DependanciesUsed: string[]
  PackagesUsed: string[]
}

export interface Interface {
  ID: string
  Name: string
  Description: string
  ImplementedBy: string[]
  Methods: FunctionDefinition[]
  Loc: Location
  DependanciesUsed: string[]
  PackagesUsed: string[]
}

export interface ExternalVar {
  ID: string
  Name: string
  Description: string
  Typing: string
  Value: unknown
  Location: Location
}

export interface File {
  Path: string
  Name: string
  Description: string
  Functions: string[]
  ExternalVars: string[]
  Structs: string[]
  Interfaces: string[]
  PackagesImported: string[]
  DependanciesImported: { PackagePath: string }[]
  FromPackage: string
}

export interface Package {
  Path: string
  Description: string
  Functions: string[]
  Structs: string[]
  Interfaces: string[]
  ExternalVars: string[]
  Files: string[]
}

export interface Dependency {
  PackagePath: string
}

export interface Topology {
  Root: string
  Packages: Record<string, Package>
  Files: Record<string, File>
  Struct: Record<string, Struct>
  Interfaces: Record<string, Interface>
  Functions: Record<string, Function>
  ExternalVars: Record<string, ExternalVar>
  Dependancies: Dependency[]
  Errors: Record<string, string>
}

export type NodeKind = 'struct' | 'function' | 'file' | 'package' | 'interface_method'

export interface VizNode {
  id: string
  kind: NodeKind
  name: string
  description: string
  fileId: string
  packageId: string
  interfaceId?: string
}

export interface VizEdge {
  id: string
  source: string
  target: string
  kind: 'ownership' | 'call' | 'use' | 'implements'
}

export interface VizData {
  nodes: VizNode[]
  edges: VizEdge[]
}
