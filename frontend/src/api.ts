import type { Topology, Function, Struct, Interface } from './types'

const BASE = ''

function getBasePath(): string {
  return BASE
}

export async function fetchTopology(): Promise<Topology> {
  const res = await fetch(`${getBasePath()}/api/topology`)
  if (!res.ok) throw new Error(`Failed to fetch topology: ${res.statusText}`)
  return res.json()
}

export interface FunctionContext {
  Function: { Function: Function; Cut: string }
  ParentStruct: { Struct: Struct; Cut: string } | null
  InterfacesUsed: InterfaceUsage[]
}

export interface InterfaceUsage {
  ID: string
  Name: string
  Description: string
  Implementations: InterfaceImplementation[]
}

export interface InterfaceImplementation {
  StructID: string
  Name: string
  Description: string
  Methods: { ID: string; Name: string; Description: string }[]
}

export interface StructContext {
  Struct: { Struct: Struct; Cut: string }
  Interfaces: { ID: string; Name: string; Description: string }[]
}

export async function fetchFunctionDetail(id: string): Promise<FunctionContext> {
  const res = await fetch(`${getBasePath()}/api/function/${encodeURIComponent(id)}`)
  if (!res.ok) throw new Error(`Failed to fetch function: ${res.statusText}`)
  return res.json()
}

export async function fetchStructDetail(id: string): Promise<StructContext> {
  const res = await fetch(`${getBasePath()}/api/struct/${encodeURIComponent(id)}`)
  if (!res.ok) throw new Error(`Failed to fetch struct: ${res.statusText}`)
  return res.json()
}
