import {
  forceSimulation,
  forceLink,
  forceManyBody,
  forceCenter,
  forceCollide,
  type SimulationNodeDatum,
  type SimulationLinkDatum,
  type Force,
} from 'd3-force'
import type { Topology, VizNode, VizEdge } from '../types'
import { buildVizData } from '../utils/graphHelpers'

interface SizedNode extends SimulationNodeDatum {
  id: string
  kind: VizNode['kind']
  name: string
  description: string
  w: number
  h: number
  fileId: string
  packageId: string
  methodFrom: string | null
  connectionCount: number
  isMethod: boolean
  structNode: boolean
}

interface SizedEdge extends SimulationLinkDatum<SizedNode> {
  kind: VizEdge['kind']
}

function estimateTextWidth(text: string, fontSize: number): number {
  return text.length * fontSize * 0.58 + 4
}

function nodeSize(node: VizNode, topo: Topology): { w: number; h: number } {
  switch (node.kind) {
    case 'struct': {
      const s = topo.Struct[node.id]
      const hasFields = s?.Params && s.Params.length > 0
      const nameW = estimateTextWidth(s?.Name || node.name, 13)
      const w = Math.max(120, nameW + 28)
      const h = hasFields ? 52 : 40
      return { w, h }
    }
    case 'function': {
      const fn = topo.Functions[node.id]
      const nameW = estimateTextWidth(fn?.Name || node.name, 12)
      const w = Math.max(100, nameW + 24)
      const h = 36
      return { w, h }
    }
    case 'interface_method': {
      return { w: 44, h: 44 }
    }
    default:
      return { w: 120, h: 36 }
  }
}

function flowerForce(
  structIds: Set<string>,
  nodeMap: Map<string, SizedNode>,
  topo: Topology,
  strength: number,
): Force<SizedNode, SizedEdge> {
  const structMethods = new Map<string, SizedNode[]>()
  const methodStruct = new Map<string, string>()

  for (const [id, node] of nodeMap) {
    if (node.kind !== 'function') continue
    const fn = topo.Functions[id]
    if (fn?.MethodFrom && structIds.has(fn.MethodFrom)) {
      if (!structMethods.has(fn.MethodFrom)) structMethods.set(fn.MethodFrom, [])
      structMethods.get(fn.MethodFrom)!.push(node)
      methodStruct.set(id, fn.MethodFrom)
    }
  }

  function force(alpha: number) {
    const a = alpha * strength

    for (const [structId, methods] of structMethods) {
      const sNode = nodeMap.get(structId)
      if (!sNode || sNode.x == null || sNode.y == null || isNaN(sNode.x) || isNaN(sNode.y)) continue
      if (methods.length === 0) continue

      const n = methods.length
      let radius: number

      if (n === 1) {
        radius = Math.max(sNode.w / 2 + 32, 70)
      } else {
        const maxMethodW = Math.max(...methods.map(m => m.w))
        const minArcLen = maxMethodW + 12
        radius = Math.max(
          (n * minArcLen) / (2 * Math.PI),
          Math.max(sNode.w, sNode.h) / 2 + 40,
        )
      }

      const cx = sNode.x
      const cy = sNode.y

      methods.forEach((m, i) => {
        if (m.x == null || m.y == null || isNaN(m.x) || isNaN(m.y)) return
        const angle = n === 1
          ? -Math.PI / 2
          : (i / n) * 2 * Math.PI - Math.PI / 2
        const tx = cx + radius * Math.cos(angle)
        const ty = cy + radius * Math.sin(angle)

        m.vx = (m.vx || 0) + (tx - m.x) * a
        m.vy = (m.vy || 0) + (ty - m.y) * a
      })
    }
  }

  return force
}

export interface LayoutNode {
  id: string
  type: string
  position: { x: number; y: number }
  parentId?: string
  style?: { width: number; height: number }
  data: Record<string, unknown>
  draggable?: boolean
  selectable?: boolean
  zIndex?: number
}

export interface LayoutEdge {
  id: string
  source: string
  target: string
  type?: string
  animated?: boolean
  style?: Record<string, unknown>
  markerEnd?: { type: string; width: number; height: number; color: string }
  zIndex?: number
}

export interface LayoutResult {
  nodes: LayoutNode[]
  edges: LayoutEdge[]
}

export function computeLayout(topology: Topology): LayoutResult {
  const { nodes: vizNodes, edges: vizEdges } = buildVizData(topology)

  const nodeMap = new Map<string, SizedNode>()
  const structIds = new Set<string>()

  const dataNodes: SizedNode[] = []

  for (const vn of vizNodes) {
    if (vn.kind === 'file' || vn.kind === 'package') continue
    const fn = topology.Functions[vn.id]
    const isMethod = !!(fn?.MethodFrom && topology.Struct[fn.MethodFrom])
    const structNode = vn.kind === 'struct'

    const connCount = vizEdges.filter(
      e => e.source === vn.id || e.target === vn.id,
    ).length

    const size = nodeSize(vn, topology)

    const sized: SizedNode = {
      id: vn.id,
      kind: vn.kind,
      name: vn.name,
      description: vn.description,
      w: size.w,
      h: size.h,
      fileId: vn.fileId,
      packageId: vn.packageId,
      methodFrom: fn?.MethodFrom || null,
      connectionCount: connCount,
      isMethod,
      structNode,
    }

    if (structNode) structIds.add(vn.id)
    nodeMap.set(vn.id, sized)
    dataNodes.push(sized)
  }

  const dataLinks: SizedEdge[] = []
  for (const edge of vizEdges) {
    const s = nodeMap.get(edge.source)
    const t = nodeMap.get(edge.target)
    if (!s || !t) continue
    dataLinks.push({ source: s, target: t, kind: edge.kind })
  }

  const maxConn = Math.max(1, ...dataNodes.map(n => n.connectionCount))

  const sim = forceSimulation<SizedNode>(dataNodes)
    .force('link', forceLink<SizedNode, SizedEdge>(dataLinks)
      .distance(80)
      .strength(0.6),
    )
    .force('charge', forceManyBody<SizedNode>()
      .strength(d => -50 - d.connectionCount * 15),
    )
    .force('collide', forceCollide<SizedNode>()
      .radius(d => Math.max(d.w, d.h) / 2 + 8)
      .strength(1),
    )
    .force('x', forceCenter().x(0).strength(0.08))
    .force('y', forceCenter().y(0).strength(0.08))
    .force('center', (() => {
      function centerForce(alpha: number) {
        for (const d of dataNodes) {
          if (d.x == null || d.y == null || isNaN(d.x) || isNaN(d.y)) continue
          const weight = d.connectionCount / maxConn
          d.vx = (d.vx || 0) - d.x * alpha * 0.08 * weight
          d.vy = (d.vy || 0) - d.y * alpha * 0.08 * weight
        }
      }
      centerForce.initialize = () => {}
      return centerForce as Force<SizedNode, SizedEdge>
    })())
    .force('flower', flowerForce(structIds, nodeMap, topology, 0.6))
    .stop()

  for (let i = 0; i < 800; i++) sim.tick()

  const layoutNodes: LayoutNode[] = []

  const layoutEdges: LayoutEdge[] = []
  for (const edge of vizEdges) {
    if (!nodeMap.has(edge.source) || !nodeMap.has(edge.target)) continue
    layoutEdges.push({
      id: edge.id,
      source: edge.source,
      target: edge.target,
      type: 'straight',
      animated: true,
      style: { stroke: '#ff4444', strokeWidth: 2.5 },
      markerEnd: { type: 'arrowclosed', width: 12, height: 12, color: '#ff4444' },
    })
  }

  console.log(
    '[ltp-layout] sim done:',
    dataNodes.length, 'nodes,',
    layoutEdges.length, 'edges',
  )

  for (const d of dataNodes) {
    if (d.x == null || d.y == null || isNaN(d.x) || isNaN(d.y)) continue

    if (d.kind === 'struct') {
      const s = topology.Struct[d.id]
      const usedByFns: string[] = []
      const usedByStructs: string[] = []
      for (const [, fn] of Object.entries(topology.Functions)) {
        if (fn.StructsUsed?.includes(d.id)) {
          if (fn.MethodFrom) {
            const ps = topology.Struct[fn.MethodFrom]
            if (ps) usedByStructs.push(ps.Name)
          } else {
            usedByFns.push(fn.Name)
          }
        }
      }

      layoutNodes.push({
        id: d.id,
        type: 'struct',
        position: { x: d.x - d.w / 2, y: d.y - d.h / 2 },
        style: { width: d.w, height: d.h },
        data: {
          name: s?.Name || d.name,
          description: s?.Description || '',
          params: s?.Params || [],
          usedByFns: [...new Set(usedByFns)],
          usedByStructs: [...new Set(usedByStructs)],
        },
        draggable: false,
        selectable: false,
        zIndex: 10,
      })
    } else if (d.kind === 'function') {
      const fn = topology.Functions[d.id]
      const structsUsed: { name: string; id: string }[] = []
      for (const sid of fn?.StructsUsed || []) {
        const st = topology.Struct[sid]
        if (st) structsUsed.push({ name: st.Name, id: st.ID })
      }

      const calledFnNames: string[] = []
      for (const fid of fn?.FunctionsUsed || []) {
        const cf = topology.Functions[fid]
        if (cf && !cf.MethodFrom) calledFnNames.push(cf.Name)
      }

      layoutNodes.push({
        id: d.id,
        type: 'function',
        position: { x: d.x - d.w / 2, y: d.y - d.h / 2 },
        style: { width: d.w, height: d.h },
        data: {
          name: fn?.Name || d.name,
          description: fn?.Description || '',
          input: fn?.Input || [],
          output: fn?.Output || [],
          isMethod: d.isMethod,
          methodFrom: d.methodFrom,
          structsUsed,
          calledFnNames: [...new Set(calledFnNames)],
        },
        draggable: false,
        selectable: false,
        zIndex: 10,
      })
    } else if (d.kind === 'interface_method') {
      const ifaceId = (vizNodes.find(vn => vn.id === d.id) as VizNode | undefined)?.interfaceId || ''
      const iface = ifaceId ? topology.Interfaces[ifaceId] : undefined
      const implementations: { structName: string; methods: { name: string; desc: string }[] }[] = []

      if (iface?.ImplementedBy) {
        for (const sid of iface.ImplementedBy) {
          const st = topology.Struct[sid]
          if (!st) continue
          const stMethods: { name: string; desc: string }[] = []
          for (const mid of st.Methods || []) {
            const mFn = topology.Functions[mid]
            if (mFn) stMethods.push({ name: mFn.Name, desc: mFn.Description })
          }
          implementations.push({ structName: st.Name, methods: stMethods })
        }
      }

      layoutNodes.push({
        id: d.id,
        type: 'interfaceMethod',
        position: { x: d.x - d.w / 2, y: d.y - d.h / 2 },
        style: { width: d.w, height: d.h },
        data: {
          name: d.name,
          interfaceName: iface?.Name || d.name,
          interfaceId: ifaceId,
          description: iface?.Description || '',
          implementations,
        },
        draggable: false,
        selectable: false,
        zIndex: 10,
      })
    }
  }

  return { nodes: layoutNodes, edges: layoutEdges }
}
