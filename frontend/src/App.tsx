import { useEffect, useState } from 'react'
import { fetchTopology } from './api'
import type { Topology } from './types'
import GraphCanvas from './components/GraphCanvas'
import { buildVizData } from './utils/graphHelpers'

function App() {
  const [topology, setTopology] = useState<Topology | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    fetchTopology()
      .then(setTopology)
      .catch(err => setError(err.message))
  }, [])

  if (error) {
    return (
      <div className="flex items-center justify-center w-full h-full bg-[#0c0c0d] font-mono text-sm text-red-400">
        <div className="text-center">
          <div className="text-5xl mb-4">!</div>
          <div className="text-base text-red-300 font-semibold mb-2">Failed to load topology</div>
          <div className="text-xs text-gray-500 max-w-md">{error}</div>
        </div>
      </div>
    )
  }

  if (!topology) {
    return (
      <div className="flex items-center justify-center w-full h-full bg-[#0c0c0d] font-mono text-sm">
        <div className="text-center">
          <div className="w-10 h-10 border-2 border-indigo-500 border-t-transparent rounded-full animate-spin mx-auto mb-4" />
          <div className="text-gray-400">Loading topology...</div>
        </div>
      </div>
    )
  }

  const fnKeys = Object.keys(topology.Functions || {})
  const stKeys = Object.keys(topology.Struct || {})
  const ifKeys = Object.keys(topology.Interfaces || {})
  const fileKeys = Object.keys(topology.Files || {})
  const pkgKeys = Object.keys(topology.Packages || {})

  const { edges: vizEdges } = buildVizData(topology)

  return (
    <div className="flex flex-col w-full h-full bg-[#0c0c0d]">
      <header
        className="flex items-center justify-between h-12 px-4 flex-shrink-0 border-b select-none"
        style={{
          background: 'linear-gradient(180deg, rgba(17,17,22,0.98) 0%, rgba(14,14,18,0.95) 100%)',
          borderColor: 'rgba(255,255,255,0.06)',
        }}
      >
        <div className="flex items-center gap-3">
          <div
            className="w-7 h-7 rounded-md flex items-center justify-center"
            style={{ background: 'linear-gradient(135deg, #6366f1 0%, #8b5cf6 100%)' }}
          >
            <span className="text-white text-xs font-bold">T</span>
          </div>
          <span className="text-sm font-semibold tracking-tight text-gray-100 font-sans">
            Topology Manager
          </span>
        </div>

        <div className="flex items-center gap-4 text-xs font-mono text-gray-500">
          <span title="Packages">
            <span className="text-[#e67e22] font-medium">{pkgKeys.length}</span> packages
          </span>
          <span title="Files">
            <span className="text-gray-400 font-medium">{fileKeys.length}</span> files
          </span>
          <span title="Structs">
            <span className="text-[#9b59b6] font-medium">{stKeys.length}</span> structs
          </span>
          <span title="Interfaces">
            <span className="text-[#db34b6] font-medium">{ifKeys.length}</span> interfaces
          </span>
          <span title="Functions">
            <span className="text-[#3498db] font-medium">{fnKeys.length}</span> functions
          </span>
          <span className="text-[10px] text-gray-600">·</span>
          <span title="Edges">
            <span className="text-gray-500 font-medium">{vizEdges.length}</span> edges
          </span>
        </div>
      </header>

      <div className="flex-1 w-full" style={{ height: 'calc(100% - 48px)' }}>
        <GraphCanvas topology={topology} />
      </div>
    </div>
  )
}

export default App
