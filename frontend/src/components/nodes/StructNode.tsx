import type { NodeProps } from '@xyflow/react'

interface StructNodeData {
  name: string
  description: string
  params: { Name: string; Typing: string }[]
  usedByFns: string[]
  usedByStructs: string[]
}

export default function StructNode({ data }: NodeProps) {
  const d = data as unknown as StructNodeData

  return (
    <div
      style={{
        width: '100%',
        height: '100%',
        background: 'linear-gradient(135deg, #e8d5f5 0%, #d4b8e8 100%)',
        border: '2px solid #9b59b6',
        borderRadius: '10px',
        padding: '6px 12px',
        boxShadow: '0 2px 10px rgba(155, 89, 182, 0.12)',
        fontFamily: 'ui-monospace, SFMono-Regular, monospace',
        cursor: 'default',
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'center',
        alignItems: 'center',
        boxSizing: 'border-box',
        overflow: 'hidden',
      }}
    >
      <div style={{ fontWeight: 700, fontSize: 13, color: '#4a235a', whiteSpace: 'nowrap' }}>
        {d.name}
      </div>
      {d.params && d.params.length > 0 && (
        <div style={{ fontSize: 9, color: '#7d3c98', opacity: 0.5, marginTop: 2 }}>
          {d.params.length}f
        </div>
      )}
    </div>
  )
}
