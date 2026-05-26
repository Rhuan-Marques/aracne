import type { NodeProps } from '@xyflow/react'

interface InterfaceMethodNodeData {
  name: string
  interfaceName: string
  description: string
  implementations: { structName: string; methods: { name: string; desc: string }[] }[]
}

export default function InterfaceMethodNode({ data }: NodeProps) {
  const d = data as unknown as InterfaceMethodNodeData

  return (
    <div
      style={{
        width: '100%',
        height: '100%',
        background: 'linear-gradient(135deg, #f5d5e8 0%, #e8b8d4 100%)',
        border: '2px solid #db34b6',
        borderRadius: '50%',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        boxShadow: '0 2px 6px rgba(219, 52, 182, 0.18)',
        cursor: 'default',
      }}
    >
      <div
        style={{
          fontSize: 7,
          fontWeight: 600,
          color: '#6c1a5a',
          fontFamily: 'ui-monospace, SFMono-Regular, monospace',
          textAlign: 'center',
          lineHeight: 1.2,
          padding: '0 2px',
          maxWidth: 36,
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
        }}
      >
        {d.name.length > 7 ? d.name.slice(0, 7) + '…' : d.name}
      </div>
    </div>
  )
}
