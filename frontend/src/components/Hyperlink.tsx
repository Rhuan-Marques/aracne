import { useCallback } from 'react'
import { useReactFlow } from '@xyflow/react'

interface HyperlinkProps {
  text: string
  kind: 'function' | 'struct' | 'extvar' | 'interface'
  nodeId?: string
}

export default function Hyperlink({ text, kind, nodeId }: HyperlinkProps) {
  const { setCenter, getNodes } = useReactFlow()

  const handleClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation()
    e.preventDefault()

    const nodes = getNodes()
    const searchName = text.replace(/^@/, '')

    let target = nodeId ? nodes.find(n => n.id === nodeId) : undefined

    if (!target) {
      target = nodes.find(n => {
        const d = n.data as Record<string, unknown>
        return (d.name as string) === searchName || d.interfaceName === searchName
      })
    }

    if (target) {
      const x = target.position.x + (typeof target.style?.width === 'number' ? target.style.width / 2 : 100)
      const y = target.position.y + (typeof target.style?.height === 'number' ? target.style.height / 2 : 30)
      setCenter(x, y, { zoom: 1.2, duration: 600 })
    }
  }, [setCenter, getNodes, nodeId])

  const colorMap: Record<string, string> = {
    function: '#3498db',
    struct: '#9b59b6',
    extvar: '#1abc9c',
    interface: '#db34b6',
  }

  return (
    <span
      onClick={handleClick}
      style={{
        color: colorMap[kind] || '#6366f1',
        cursor: 'pointer',
        textDecoration: 'underline',
        textUnderlineOffset: '2px',
        fontWeight: 500,
      }}
      title={`Navigate to ${text}`}
    >
      {text}
    </span>
  )
}
