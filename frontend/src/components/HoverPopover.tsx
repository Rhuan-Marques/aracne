import { useState, useEffect, useRef, useCallback } from 'react'
import { useReactFlow } from '@xyflow/react'
import { fetchFunctionDetail, fetchStructDetail } from '../api'
import type { Function, Struct, Interface, ExternalVar } from '../types'
import Hyperlink from './Hyperlink'

interface HoverData {
  id: string
  kind: 'struct' | 'function' | 'file' | 'package' | 'interface_method'
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

interface HoverPopoverProps {
  data: HoverData | null
  position: { x: number; y: number }
}

interface FunctionDetail {
  Function: { Function: Function; Cut: string }
  ParentStruct: { Struct: Struct; Cut: string } | null
  InterfacesUsed: {
    ID: string
    Name: string
    Description: string
    Implementations: { StructID: string; Name: string; Description: string; Methods: { ID: string; Name: string; Description: string }[] }[]
  }[]
}

export default function HoverPopover({ data, position }: HoverPopoverProps) {
  const [detailData, setDetailData] = useState<FunctionDetail | { Struct: { Struct: Struct; Cut: string } } | null>(null)
  const [loading, setLoading] = useState(false)
  const popoverRef = useRef<HTMLDivElement>(null)
  const { getNodes } = useReactFlow()

  useEffect(() => {
    setDetailData(null)
    if (!data) return

    if (data.kind === 'function') {
      setLoading(true)
      fetchFunctionDetail(data.id)
        .then(d => setDetailData(d))
        .catch(() => setDetailData(null))
        .finally(() => setLoading(false))
    } else if (data.kind === 'struct') {
      setLoading(true)
      fetchStructDetail(data.id)
        .then(d => setDetailData(d as unknown as { Struct: { Struct: Struct; Cut: string } }))
        .catch(() => setDetailData(null))
        .finally(() => setLoading(false))
    }
  }, [data])

  const getUsedBy = useCallback((elementId: string) => {
    const fns: string[] = []
    const structs: string[] = []
    const fnMap = data?.topology.functions || {}
    const stMap = data?.topology.structs || {}

    for (const [, fn] of Object.entries(fnMap)) {
      if (fn.StructsUsed?.includes(elementId) || fn.InterfacesUsed?.includes(elementId)) {
        if (fn.MethodFrom && stMap[fn.MethodFrom]) {
          structs.push(stMap[fn.MethodFrom].Name)
        } else {
          fns.push(fn.Name)
        }
      }
    }

    const extVarLoc = fnMap[elementId]?.Loc?.Path

    return { fns: [...new Set(fns)], structs: [...new Set(structs)], extVarFile: extVarLoc }
  }, [data])

  if (!data) return null

  const funcDetail = detailData as FunctionDetail | null
  const structDetail = detailData as { Struct: { Struct: Struct; Cut: string } } | null
  const nodes = getNodes()

  const nodeCenterX = data.rect.left + data.rect.width / 2
  const nodeCenterY = data.rect.top + data.rect.height / 2

  const popStyle: React.CSSProperties = {
    position: 'fixed',
    left: Math.min(position.x + 16, window.innerWidth - 420),
    top: Math.min(position.y + 16, window.innerHeight - 500),
    zIndex: 1000,
    width: 380,
    maxHeight: 480,
    overflow: 'auto',
    background: 'rgba(20, 20, 28, 0.97)',
    backdropFilter: 'blur(20px)',
    border: '1px solid rgba(255,255,255,0.1)',
    borderRadius: '12px',
    padding: '16px',
    boxShadow: '0 8px 40px rgba(0,0,0,0.5)',
    fontSize: '12px',
    lineHeight: 1.6,
    color: '#d0d0d0',
  }

  const renderMentions = (text: string) => {
    if (!text) return null
    const parts = text.split(/(@\w+\.?\w*)/g)
    return parts.map((part, i) => {
      if (part.startsWith('@')) {
        const name = part.slice(1)
        const fnNode = nodes.find(n => {
          const d = n.data as Record<string, unknown>
          return (d.name as string) === name
        })
        const kind = fnNode?.type === 'struct' ? 'struct' : fnNode?.type === 'function' ? 'function' : 'function'
        return <Hyperlink key={i} text={part} kind={kind as 'function' | 'struct' | 'interface'} nodeId={fnNode?.id} />
      }
      return <span key={i}>{part}</span>
    })
  }

  const topo = data.topology

  if (data.kind === 'struct') {
    const s = topo.structs[data.id]
    const usedBy = getUsedBy(data.id)

    return (
      <div ref={popoverRef} style={popStyle}>
        <div style={{ fontWeight: 700, fontSize: 14, color: '#9b59b6', marginBottom: 8 }}>
          ▣ struct {data.id.split('/').pop()}
        </div>

        {(s?.Description || (structDetail?.Struct?.Struct?.Description)) && (
          <div style={{ color: '#b0b0b0', marginBottom: 10 }}>
            {renderMentions(s?.Description || structDetail?.Struct?.Struct?.Description || '')}
          </div>
        )}

        {structDetail && (
          <pre style={{
            background: 'rgba(0,0,0,0.3)',
            borderRadius: 8,
            padding: '10px',
            fontSize: 11,
            overflow: 'auto',
            marginBottom: 10,
            border: '1px solid rgba(255,255,255,0.05)',
            fontFamily: 'ui-monospace, SFMono-Regular, monospace',
          }}>
            {structDetail.Struct.Cut.slice(0, 800)}
            {structDetail.Struct.Cut.length > 800 ? '\n// ...' : ''}
          </pre>
        )}

        {s?.Params && s.Params.length > 0 && (
          <div style={{ marginBottom: 8 }}>
            <div style={{ fontWeight: 600, color: '#e0e0e0', marginBottom: 4 }}>Fields</div>
            {s.Params.map((p, i) => (
              <div key={i} style={{ fontFamily: 'monospace', color: '#888' }}>
                var {p.Name} {p.Typing}
              </div>
            ))}
          </div>
        )}

        <div style={{ fontWeight: 600, color: '#e0e0e0', marginBottom: 4 }}>Used by</div>
        <div style={{ color: '#888' }}>
          {usedBy.fns.length === 0 && usedBy.structs.length === 0 && <span style={{ opacity: 0.5 }}>(none)</span>}
          {usedBy.fns.map((fn, i) => (
            <span key={`fn-${i}`}>
              <Hyperlink text={`@${fn}`} kind="function" />
              {i < usedBy.fns.length - 1 && ', '}
            </span>
          ))}
          {usedBy.fns.length > 0 && usedBy.structs.length > 0 && ', '}
          {usedBy.structs.map((st, i) => (
            <span key={`st-${i}`}>
              <Hyperlink text={`@${st}`} kind="struct" />
              {i < usedBy.structs.length - 1 && ', '}
            </span>
          ))}
        </div>

        {loading && <div style={{ color: '#666', marginTop: 8 }}>Loading details...</div>}
      </div>
    )
  }

  if (data.kind === 'function') {
    const fn = topo.functions[data.id]

    const inputStr = (fn?.Input || funcDetail?.Function?.Function?.Input || [])
      .map((p: { Name: string; Typing: string }) => `${p.Name} ${p.Typing}`)
      .join(', ')
    const outputStr = (fn?.Output || funcDetail?.Function?.Function?.Output || [])
      .map((p: { Name: string; Typing: string }) => p.Typing || p.Name)
      .join(', ')

    const parentStructName = funcDetail?.ParentStruct?.Struct?.Name

    const structsUsedAsObject = (fn?.StructsUsed || []).filter(sid => {
      const s = topo.structs[sid]
      if (!s) return false
      return !(fn?.FunctionsUsed || []).some(fid => {
        const tfn = topo.functions[fid]
        return tfn?.MethodFrom === sid
      })
    }).map(sid => topo.structs[sid]?.Name || sid)

    return (
      <div ref={popoverRef} style={popStyle}>
        <div style={{ fontWeight: 700, fontSize: 14, color: '#3498db', marginBottom: 8 }}>
          ◉ func {data.id.split('/').pop()}
          {parentStructName && <span style={{ color: '#888', fontWeight: 400 }}> · on {parentStructName}</span>}
        </div>

        {(fn?.Description || funcDetail?.Function?.Function?.Description) && (
          <div style={{ color: '#b0b0b0', marginBottom: 10 }}>
            {renderMentions((fn?.Description || funcDetail?.Function?.Function?.Description) || '')}
          </div>
        )}

        <pre style={{
          background: 'rgba(0,0,0,0.3)',
          borderRadius: 8,
          padding: '10px',
          fontSize: 11,
          overflow: 'auto',
          marginBottom: 10,
          border: '1px solid rgba(255,255,255,0.05)',
          fontFamily: 'ui-monospace, SFMono-Regular, monospace',
          color: '#ccc',
        }}>
          func {data.id.split('/').pop()}({inputStr})
          {outputStr ? ` ${outputStr}` : ''}
        </pre>

        {funcDetail?.Function?.Cut && (
          <pre style={{
            background: 'rgba(0,0,0,0.3)',
            borderRadius: 8,
            padding: '10px',
            fontSize: 11,
            overflow: 'auto',
            marginBottom: 10,
            border: '1px solid rgba(255,255,255,0.05)',
            fontFamily: 'ui-monospace, SFMono-Regular, monospace',
            maxHeight: 200,
          }}>
            {funcDetail.Function.Cut.length > 600
              ? funcDetail.Function.Cut.slice(0, 600) + '\n// ...'
              : funcDetail.Function.Cut}
          </pre>
        )}

        {structsUsedAsObject.length > 0 && (
          <div>
            <div style={{ fontWeight: 600, color: '#e0e0e0', marginBottom: 4 }}>Uses Structs</div>
            <div style={{ color: '#888' }}>
              {structsUsedAsObject.map((name, i) => (
                <span key={i}>
                  <Hyperlink text={`@${name}`} kind="struct" />
                  {i < structsUsedAsObject.length - 1 && ', '}
                </span>
              ))}
            </div>
          </div>
        )}

        {funcDetail?.InterfacesUsed && funcDetail.InterfacesUsed.length > 0 && (
          <div style={{ marginTop: 12 }}>
            <div style={{ fontWeight: 600, color: '#db34b6', marginBottom: 4 }}>Interface calls</div>
            {funcDetail.InterfacesUsed.map((iu, i) => (
              <div key={i} style={{ marginBottom: 8 }}>
                <Hyperlink text={`@${iu.Name}`} kind="interface" />
                {iu.Implementations.map((impl, j) => (
                  <div key={j} style={{ marginLeft: 12, color: '#888' }}>
                    ↳ <Hyperlink text={`@${impl.Name}`} kind="struct" />
                    {impl.Methods.map((m, k) => (
                      <span key={k} style={{ marginLeft: 8 }}>
                        <Hyperlink text={`@${m.Name}`} kind="function" />
                      </span>
                    ))}
                  </div>
                ))}
              </div>
            ))}
          </div>
        )}

        {loading && <div style={{ color: '#666', marginTop: 8 }}>Loading details...</div>}
      </div>
    )
  }

  if (data.kind === 'interface_method') {
    const impls = (data.nodeData?.implementations as Array<{ structName: string; methods: Array<{ name: string; desc: string }> }>) || []
    const interfaceName = data.nodeData?.interfaceName as string || data.id

    return (
      <div ref={popoverRef} style={popStyle}>
        <div style={{ fontWeight: 700, fontSize: 14, color: '#db34b6', marginBottom: 8 }}>
          ● method {interfaceName}
        </div>

        <div style={{ color: '#b0b0b0', marginBottom: 10 }}>
          {(data.nodeData?.description as string) || 'Interface method call'}
        </div>

        {impls.length > 0 ? (
          <div>
            <div style={{ fontWeight: 600, color: '#e0e0e0', marginBottom: 6 }}>Implemented By</div>
            {impls.map((impl) => (
              <div key={impl.structName} style={{ marginBottom: 10, paddingLeft: 8, borderLeft: '2px solid #9b59b6' }}>
                <div style={{ fontWeight: 600, color: '#9b59b6' }}>
                  <Hyperlink text={`@${impl.structName}`} kind="struct" />
                </div>
                {impl.methods.map((m) => (
                  <div key={m.name} style={{ marginLeft: 12, color: '#888', fontSize: 11 }}>
                    ↳ <Hyperlink text={`@${m.name}`} kind="function" />
                    <span style={{ marginLeft: 8 }}>{m.desc}</span>
                  </div>
                ))}
              </div>
            ))}
          </div>
        ) : (
          <div style={{ color: '#666', fontStyle: 'italic' }}>
            No implementations found
          </div>
        )}
      </div>
    )
  }

  if (data.kind === 'file') {
    const f = topo.files[data.id]
    if (!f) return null

    const extVarsInFile = (f.ExternalVars || []).map(eid => topo.externals[eid]).filter(Boolean)

    return (
      <div ref={popoverRef} style={popStyle}>
        <div style={{ fontWeight: 700, fontSize: 14, color: '#a0a0aa', marginBottom: 8 }}>
          ▢ file {f.Name}
        </div>

        {f.Description && (
          <div style={{ color: '#b0b0b0', marginBottom: 10 }}>
            {f.Description}
          </div>
        )}

        {extVarsInFile.length > 0 && (
          <div>
            <div style={{ fontWeight: 600, color: '#1abc9c', marginBottom: 6 }}>External Vars</div>
            {extVarsInFile.map(ev => (
              <div key={ev.ID} style={{ marginBottom: 10, paddingLeft: 8 }}>
                <div style={{ fontWeight: 600, color: '#1abc9c' }}>{ev.Name}: {String(ev.Value ?? ev.Typing)}</div>
                {ev.Description && <div style={{ color: '#888', fontSize: 11, marginTop: 2 }}>{ev.Description}</div>}
                <div style={{ color: '#666', fontSize: 11, marginTop: 2 }}>
                  Used by: {}
                  {(() => {
                    const ub = getUsedBy(ev.ID)
                    const all = [...ub.fns.map(fn => ({ name: fn, kind: 'function' as const })), ...ub.structs.map(s => ({ name: s, kind: 'struct' as const }))]
                    return all.length === 0 ? <span style={{ opacity: 0.5 }}>(none)</span> : all.map((u, i) => (
                      <span key={i}>
                        <Hyperlink text={`@${u.name}`} kind={u.kind} />
                        {i < all.length - 1 && ', '}
                      </span>
                    ))
                  })()}
                </div>
              </div>
            ))}
          </div>
        )}

        {extVarsInFile.length === 0 && (
          <div style={{ color: '#666', fontStyle: 'italic' }}>No external variables in this file</div>
        )}
      </div>
    )
  }

  if (data.kind === 'package') {
    const pkg = topo.packages[data.id]

    const usedBy = getUsedBy(data.id)

    return (
      <div ref={popoverRef} style={popStyle}>
        <div style={{ fontWeight: 700, fontSize: 14, color: '#e67e22', marginBottom: 8 }}>
          ▭ package {data.id}
        </div>

        {pkg?.Description && (
          <div style={{ color: '#b0b0b0', marginBottom: 10 }}>
            {pkg.Description}
          </div>
        )}

        <div style={{ fontWeight: 600, color: '#e0e0e0', marginBottom: 4 }}>Used by</div>
        <div style={{ color: '#888' }}>
          {usedBy.fns.length === 0 && usedBy.structs.length === 0 && <span style={{ opacity: 0.5 }}>(none)</span>}
          {usedBy.fns.map((fn, i) => (
            <span key={i}>
              <Hyperlink text={`@${fn}`} kind="function" />
              {i < usedBy.fns.length - 1 && ', '}
            </span>
          ))}
          {usedBy.fns.length > 0 && usedBy.structs.length > 0 && ', '}
          {usedBy.structs.map((st, i) => (
            <span key={i}>
              <Hyperlink text={`@${st}`} kind="struct" />
              {i < usedBy.structs.length - 1 && ', '}
            </span>
          ))}
        </div>
      </div>
    )
  }

  return null
}
