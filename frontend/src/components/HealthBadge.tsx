import { useEffect, useState } from 'react'

type Status = 'unknown' | 'healthy' | 'unreachable'

export default function HealthBadge() {
  const [status, setStatus] = useState<Status>('unknown')
  const [message, setMessage] = useState<string>('Checking...')

  const check = async () => {
    try {
      // Use relative path for health check, handled by Vite proxy
      const res = await fetch(`/api/health`, { cache: 'no-store' })
      const txt = await res.text()
      if (res.ok && txt.trim() === 'ok') {
        setStatus('healthy')
        setMessage('200 ok')
      } else {
        setStatus('unreachable')
        setMessage(`${res.status} ${txt}`)
      }
    } catch (e: any) {
      setStatus('unreachable')
      setMessage(e?.message || 'error')
    }
  }

  useEffect(() => {
    check()
    const id = setInterval(check, 10000)
    return () => clearInterval(id)
  }, [])

  const color =
    status === 'healthy' ? '#16a34a' :
    status === 'unreachable' ? '#dc2626' :
    '#6b7280'

  const bg =
    status === 'healthy' ? '#dcfce7' :
    status === 'unreachable' ? '#fee2e2' :
    '#e5e7eb'

  return (
    <span
      title={message}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 8,
        padding: '4px 10px',
        borderRadius: 999,
        background: bg,
        color,
        fontSize: 12,
        fontWeight: 600,
      }}
    >
      <span
        style={{
          width: 8,
          height: 8,
          borderRadius: 999,
          background: color,
          display: 'inline-block',
        }}
      />
      {status === 'healthy' ? 'Healthy' : status === 'unreachable' ? 'Unreachable' : 'Checking...'}
    </span>
  )
}
