import type { NodeProps } from '@xyflow/react'

interface FileNodeData {
  name: string
  description: string
}

export default function FileNode({ data }: NodeProps) {
  const d = data as unknown as FileNodeData

  return (
    <div
      style={{
        width: '100%',
        height: '100%',
        background: 'rgba(180, 180, 195, 0.04)',
        border: '1px dashed rgba(140, 140, 155, 0.18)',
        borderRadius: '50%',
        position: 'relative',
        pointerEvents: 'none',
      }}
    >
      <div
        style={{
          position: 'absolute',
          left: '50%',
          top: 8,
          transform: 'translateX(-50%)',
          fontSize: 10,
          fontFamily: 'ui-monospace, SFMono-Regular, monospace',
          color: 'rgba(160, 160, 175, 0.45)',
          fontWeight: 500,
          whiteSpace: 'nowrap',
          pointerEvents: 'auto',
        }}
      >
        {d.name}
      </div>
    </div>
  )
}
