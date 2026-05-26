import type { NodeProps } from '@xyflow/react'

interface PackageNodeData {
  name: string
  description: string
}

export default function PackageNode({ data }: NodeProps) {
  const d = data as unknown as PackageNodeData

  return (
    <div
      style={{
        width: '100%',
        height: '100%',
        background: 'transparent',
        border: '1.5px solid rgba(230, 126, 34, 0.25)',
        borderRadius: '50%',
        position: 'relative',
        pointerEvents: 'none',
      }}
    >
      <div
        style={{
          position: 'absolute',
          left: '50%',
          top: '50%',
          transform: 'translate(-50%, -50%)',
          fontSize: 10,
          fontFamily: 'ui-monospace, SFMono-Regular, monospace',
          color: 'rgba(230, 126, 34, 0.45)',
          fontWeight: 500,
          whiteSpace: 'nowrap',
          pointerEvents: 'auto',
          userSelect: 'none',
        }}
      >
        {d.name}
      </div>
    </div>
  )
}
