import { useState, useCallback } from 'react'
import { useReactFlow } from '@xyflow/react'

export default function SearchBar() {
  const [query, setQuery] = useState('')
  const { setCenter, getNodes } = useReactFlow()

  const handleSearch = useCallback((e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key !== 'Enter' || !query.trim()) return

    const nodes = getNodes()
    const q = query.trim().toLowerCase()

    const match = nodes.find(n => {
      const d = n.data as Record<string, unknown>
      const name = (d.name as string || '').toLowerCase()
      const ifaceName = (d.interfaceName as string || '').toLowerCase()
      return name === q || ifaceName === q || name.includes(q)
    })

    if (match) {
      const x = match.position.x + (typeof match.style?.width === 'number' ? match.style.width / 2 : 100)
      const y = match.position.y + (typeof match.style?.height === 'number' ? match.style.height / 2 : 30)
      setCenter(x, y, { zoom: 1.5, duration: 600 })
    }
  }, [query, setCenter, getNodes])

  return (
    <div
      style={{
        position: 'absolute',
        top: 16,
        left: '50%',
        transform: 'translateX(-50%)',
        zIndex: 100,
        display: 'flex',
        gap: 8,
      }}
    >
      <input
        type="text"
        value={query}
        onChange={e => setQuery(e.target.value)}
        onKeyDown={handleSearch}
        placeholder="Jump to function or struct..."
        style={{
          background: 'rgba(20, 20, 28, 0.9)',
          backdropFilter: 'blur(10px)',
          border: '1px solid rgba(255,255,255,0.12)',
          borderRadius: '10px',
          padding: '10px 16px',
          width: 320,
          fontSize: 13,
          color: '#e0e0e0',
          outline: 'none',
          fontFamily: 'ui-monospace, SFMono-Regular, monospace',
        }}
      />
    </div>
  )
}
