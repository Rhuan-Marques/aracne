import { useCallback, useEffect, useMemo, useState, useRef } from 'react'
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  Controls,
  MiniMap,
  BackgroundVariant,
  MarkerType,
  useNodesState,
  useEdgesState,
  type Edge,
  type Node,
  type NodeMouseHandler,
  type ReactFlowInstance,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'

import type { Topology, Function, Struct, Interface, ExternalVar } from '../types'
import { computeLayout } from '../layout/layoutEngine'
import StructNode from './nodes/StructNode'
import FunctionNode from './nodes/FunctionNode'
import InterfaceMethodNode from './nodes/InterfaceMethodNode'
import HoverPopover from './HoverPopover'
import SearchBar from './SearchBar'

const nodeTypes = {
  struct: StructNode,
  function: FunctionNode,
  interfaceMethod: InterfaceMethodNode,
}

interface GraphCanvasProps {
  topology: Topology
}

function GraphCanvasInner({ topology }: GraphCanvasProps) {
  const { nodes: layoutNodes, edges: layoutEdges } = useMemo(
    () => {
      try {
        return computeLayout(topology)
      } catch (err) {
        console.error('Layout computation failed:', err)
        return { nodes: [], edges: [] }
      }
    },
    [topology],
  )

  const [nodes, setNodes, onNodesChange] = useNodesState(layoutNodes as Node[])
  const [edges, setEdges, onEdgesChange] = useEdgesState(layoutEdges as Edge[])

  useEffect(() => {
    setNodes(layoutNodes as Node[])
    setEdges(layoutEdges as Edge[])
  }, [layoutNodes, setNodes, layoutEdges, setEdges])
  const [hoverData, setHoverData] = useState<HoverPopoverData | null>(null)
  const [hoverPos, setHoverPos] = useState({ x: 0, y: 0 })
  const rfInstanceRef = useRef<ReactFlowInstance | null>(null)
  const hoverTimeoutRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  const miniMapNodeColor = useCallback((n: Node) => {
    switch (n.type) {
      case 'struct': return '#9b59b6'
      case 'function': return '#3498db'
      case 'interfaceMethod': return '#db34b6'
      default: return '#666'
    }
  }, [])

  const handleNodeMouseEnter: NodeMouseHandler = useCallback((_event, node) => {
    clearTimeout(hoverTimeoutRef.current)

    hoverTimeoutRef.current = setTimeout(() => {
      const el = document.querySelector(`[data-id="${node.id}"]`)
      if (!el) return

      const rect = el.getBoundingClientRect()

      setHoverData({
        id: node.id,
        kind: node.type as 'struct' | 'function' | 'interface_method',
        rect,
        topology: {
          functions: topology.Functions,
          structs: topology.Struct,
          interfaces: topology.Interfaces,
          externals: topology.ExternalVars,
          files: topology.Files,
          packages: topology.Packages,
        } as HoverPopoverData['topology'],
        nodeData: node.data as Record<string, unknown>,
      })

      setHoverPos({ x: rect.right + 8, y: rect.top })
    }, 300)
  }, [topology])

  const handleNodeMouseLeave = useCallback(() => {
    clearTimeout(hoverTimeoutRef.current)
    setHoverData(null)
  }, [])

  const handlePaneClick = useCallback(() => {
    setHoverData(null)
  }, [])

  const handleInit = useCallback((instance: ReactFlowInstance) => {
    rfInstanceRef.current = instance
    if (layoutNodes.length > 0) {
      const centerX = layoutNodes.reduce((s, n) => s + n.position.x, 0) / layoutNodes.length
      const centerY = layoutNodes.reduce((s, n) => s + n.position.y, 0) / layoutNodes.length
      instance.setCenter(centerX, centerY, { zoom: 0.6, duration: 800 })
    }
  }, [layoutNodes])

  return (
    <div style={{ width: '100%', height: '100%', position: 'relative' }}>
      <SearchBar />
      <HoverPopover data={hoverData} position={hoverPos} />

      <div style={{
        position: 'absolute',
        bottom: 12,
        right: 12,
        zIndex: 1000,
        background: 'rgba(0,0,0,0.7)',
        color: '#ff4444',
        padding: '6px 12px',
        borderRadius: 6,
        fontFamily: 'monospace',
        fontSize: 11,
      }}>
        Nodes: {layoutNodes.length} | Edges: {layoutEdges.length}
      </div>

      <ReactFlow
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onNodeMouseEnter={handleNodeMouseEnter}
        onNodeMouseLeave={handleNodeMouseLeave}
        onPaneClick={handlePaneClick}
        onInit={handleInit}
        nodeTypes={nodeTypes}
        fitView={false}
        minZoom={0.05}
        maxZoom={2.5}
        defaultViewport={{ x: 0, y: 0, zoom: 0.6 }}
        proOptions={{ hideAttribution: true }}
        style={{ background: '#0c0c0d' }}
        nodesDraggable={false}
        nodesConnectable={false}
        nodesFocusable={false}
        edgesReconnectable={false}
        defaultEdgeOptions={{
          type: 'straight',
          style: { stroke: '#ff4444', strokeWidth: 2 },
          animated: true,
          markerEnd: { type: MarkerType.ArrowClosed, width: 12, height: 12, color: '#ff4444' },
        }}
      >
        <Background variant={BackgroundVariant.Dots} gap={24} size={1} color="rgba(255,255,255,0.04)" />
        <Controls
          className="[&>button]:!bg-gray-800 [&>button]:!border-gray-700 [&>button]:!text-gray-300"
          style={{ background: 'transparent', border: 'none' }}
        />
        <MiniMap
          nodeColor={miniMapNodeColor}
          maskColor="rgba(0,0,0,0.7)"
          style={{ background: 'rgba(20,20,28,0.8)', border: '1px solid rgba(255,255,255,0.08)' }}
          pannable
          zoomable
        />
      </ReactFlow>
    </div>
  )
}

export default function GraphCanvas({ topology }: GraphCanvasProps) {
  return (
    <ReactFlowProvider>
      <GraphCanvasInner topology={topology} />
    </ReactFlowProvider>
  )
}

interface HoverPopoverData {
  id: string
  kind: 'struct' | 'function' | 'interface_method'
  rect: DOMRect
  topology: {
    functions: Record<string, Function>
    structs: Record<string, Struct>
    interfaces: Record<string, Interface>
    externals: Record<string, ExternalVar>
    files: Record<string, { Name: string; Description: string; ExternalVars: string[]; FromPackage: string }>
    packages: Record<string, { Description: string }>
  }
  nodeData?: Record<string, unknown>
}
