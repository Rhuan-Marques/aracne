import type { NodeProps } from '@xyflow/react'

interface FunctionNodeData {
  name: string
  description: string
  input: { Name: string; Typing: string }[]
  output: { Name: string; Typing: string }[]
  isMethod: boolean
  methodFrom?: string
  structsUsed: { name: string; id: string }[]
  calledFnNames: string[]
}

export default function FunctionNode({ data }: NodeProps) {
  const d = data as unknown as FunctionNodeData

  return (
    <div
      style={{
        width: '100%',
        height: '100%',
        background: 'linear-gradient(135deg, #d5e8f5 0%, #b8d8e8 100%)',
        border: '2px solid #3498db',
        borderRadius: '18px',
        padding: '6px 14px',
        boxShadow: '0 2px 10px rgba(52, 152, 219, 0.12)',
        fontFamily: 'ui-monospace, SFMono-Regular, monospace',
        cursor: 'default',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        boxSizing: 'border-box',
        overflow: 'hidden',
      }}
    >
      <div style={{ fontWeight: 700, fontSize: 12, color: '#1a5276', whiteSpace: 'nowrap' }}>
        {d.name}
      </div>
    </div>
  )
}
