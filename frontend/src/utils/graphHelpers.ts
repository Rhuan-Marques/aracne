import type { Topology, VizNode, VizEdge } from '../types'

export function buildVizData(topo: Topology): { nodes: VizNode[]; edges: VizEdge[] } {
  const nodes: VizNode[] = []
  const edges: VizEdge[] = []
  const edgeSet = new Set<string>()

  function addEdge(source: string, target: string, kind: VizEdge['kind']) {
    const key = `${source}→${target}→${kind}`
    if (edgeSet.has(key)) return
    edgeSet.add(key)
    edges.push({ id: `e-${edges.length}`, source, target, kind })
  }

  function findFileId(locPath: string): string {
    for (const [fid, file] of Object.entries(topo.Files)) {
      if (file.Path === locPath) return fid
    }
    return locPath
  }

  for (const [pkgId, pkg] of Object.entries(topo.Packages)) {
    nodes.push({
      id: pkgId,
      kind: 'package',
      name: pkgId,
      description: pkg.Description || '',
      fileId: '',
      packageId: pkgId,
    })
  }

  for (const [fileId, file] of Object.entries(topo.Files)) {
    nodes.push({
      id: fileId,
      kind: 'file',
      name: file.Name,
      description: file.Description || '',
      fileId,
      packageId: file.FromPackage,
    })
  }

  for (const [id, s] of Object.entries(topo.Struct)) {
    nodes.push({
      id,
      kind: 'struct',
      name: s.Name,
      description: s.Description || '',
      fileId: id in topo.Files ? id : findFileId(s.Loc.Path),
      packageId: findPackageId(topo, id),
    })

    if (s.Implements) {
      addEdge(id, s.Implements, 'implements')
    }
  }

  for (const [id, fn] of Object.entries(topo.Functions)) {
    nodes.push({
      id,
      kind: 'function',
      name: fn.Name,
      description: fn.Description || '',
      fileId: findFileId(fn.Loc.Path),
      packageId: findPackageId(topo, id),
    })

    if (fn.MethodFrom) {
      addEdge(fn.MethodFrom, id, 'ownership')
    }

    for (const calledId of fn.FunctionsUsed || []) {
      addEdge(id, calledId, 'call')
    }

    for (const structId of fn.StructsUsed || []) {
      const isUsedAsObject = !fn.FunctionsUsed?.some(fid => {
        const targetFn = topo.Functions[fid]
        return targetFn?.MethodFrom === structId
      })
      if (isUsedAsObject) {
        addEdge(id, structId, 'use')
      }
    }

    for (const ifaceId of fn.InterfacesUsed || []) {
      const iface = topo.Interfaces[ifaceId]
      if (!iface) continue

      const ifaceMethodNodeId = `ifacemethod-${id}-${ifaceId}`
      nodes.push({
        id: ifaceMethodNodeId,
        kind: 'interface_method',
        name: iface.Name,
        description: iface.Description || '',
        fileId: findFileId(fn.Loc.Path),
        packageId: findPackageId(topo, id),
        interfaceId: ifaceId,
      })

      addEdge(id, ifaceMethodNodeId, 'call')
    }
  }

  console.log(
    '[ltp-viz-data] built',
    nodes.length, 'nodes and',
    edges.length, 'edges from topology',
  )

  return { nodes, edges }
}

function findPackageId(topo: Topology, elementId: string): string {
  for (const [pkgId, pkg] of Object.entries(topo.Packages)) {
    if (pkg.Functions?.includes(elementId)) return pkgId
    if (pkg.Structs?.includes(elementId)) return pkgId
    if (pkg.Interfaces?.includes(elementId)) return pkgId
    if (pkg.ExternalVars?.includes(elementId)) return pkgId
  }
  return ''
}
