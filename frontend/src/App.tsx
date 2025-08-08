import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import WebSocketService from './services/WebSocketService'
import './App.css'
import HealthBadge from './components/HealthBadge'

// Config
// API and WebSocket bases are relative; Vite proxy handles /api and /ws.
const WS_PROTOCOL = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
const WS_BASE = `${WS_PROTOCOL}//${window.location.host}`

type ServerMsg =
  | { type: 'ack'; ackType: string; revision?: number }
  | { type: 'compileQueued'; jobId: string; revision: number }
  | { type: 'compileStarted'; jobId: string; revision: number; startedAt: string }
  | { type: 'compileProgress'; jobId: string; revision: number; message: string }
  | { type: 'compileSucceeded'; jobId: string; revision: number; outputPath: string; finishedAt: string }
  | { type: 'compileFailed'; jobId: string; revision: number; error: string; finishedAt: string }
  | { type: 'compileCanceled'; jobId: string; revision: number; finishedAt: string }
  | { type: 'pong' }

function useDebounce() {
  const handle = useRef<number | null>(null)
  const debounce = useCallback((fn: () => void, ms: number) => {
    if (handle.current) window.clearTimeout(handle.current)
    handle.current = window.setTimeout(fn, ms)
  }, [])
  const clear = useCallback(() => {
    if (handle.current) window.clearTimeout(handle.current)
    handle.current = null
  }, [])
  return { debounce, clear }
}

function App() {
  const [health, setHealth] = useState<string>('loading...')
  const [projectId, setProjectId] = useState<string | null>(null)
  const [content, setContent] = useState<string>('% Start typing LaTeX here...\n\\documentclass{article}\n\\begin{document}\nHello, LaTeX!\n\\end{document}\n')
  const [revision, setRevision] = useState(0)
  const [status, setStatus] = useState<string>('Idle')
  const [logs, setLogs] = useState<string[]>([])
  const [pdfKey, setPdfKey] = useState(0)

  const wsService = useRef<WebSocketService | null>(null)

  useEffect(() => {
    fetch(`/api/health`)
      .then(async r => {
        const t = await r.text()
        setHealth(`${r.status} ${t}`)
      })
      .catch(e => setHealth(`error: ${e}`))
  }, [])

  useEffect(() => {
    console.debug('API health:', health)
  }, [health])

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const r = await fetch(`/api/projects`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name: 'live-session' }),
        })
        const js = await r.json()
        if (!cancelled) {
          setProjectId(js.id)
          try {
            const fr = await fetch(`/api/projects/${js.id}/files?path=main.tex`)
            if (fr.ok) {
              const fj = await fr.json()
              if (fj && typeof fj.content === 'string') {
                setContent(fj.content)
              }
            }
          } catch {/* ignore */}
        }
      } catch (e) {
        setStatus(`Project error: ${e}`)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    if (!projectId) return
    const url = `${WS_BASE}/ws/projects/${projectId}`
    wsService.current = new WebSocketService(url, {
      onOpen: () => {
        setStatus('Connected')
        // Kick initial doc update; compile will be triggered on ack
        const initialRev = revision || 1
        wsService.current?.sendMessage({ type: 'docUpdate', path: 'main.tex', content, revision: initialRev })
      },
      onMessage: (msg: ServerMsg) => {
        switch (msg.type) {
          case 'ack':
            {
              // Accept number or numeric string
              const rv: any = (msg as any).revision
              const op: any = (msg as any).op
              if (typeof rv === 'number') setRevision(rv)
              else if (typeof rv === 'string') {
                const n = parseInt(rv, 10)
                if (!Number.isNaN(n)) setRevision(n)
              }
              // After docUpdate ack, trigger compile using ack's revision to avoid race
              const compileRev = typeof rv === 'number' ? rv : (typeof rv === 'string' ? parseInt(rv, 10) : undefined)
              if (op === 'docUpdate' && typeof compileRev === 'number' && !Number.isNaN(compileRev)) {
                wsService.current?.sendMessage({ type: 'requestCompile', path: 'main.tex', revision: compileRev })
              }
            }
            break
          case 'compileQueued':
            setStatus(`Queued r${msg.revision}`)
            break
          case 'compileStarted':
            setStatus(`Compiling r${msg.revision}...`)
            setLogs(l => [...l, `Started at ${msg.startedAt}`])
            break
          case 'compileProgress':
            setLogs(l => [...l, msg.message])
            break
          case 'compileSucceeded':
            setStatus(`Succeeded r${msg.revision}`)
            setLogs(l => [...l, `Finished at ${msg.finishedAt}`])
            setPdfKey(k => k + 1)
            break
          case 'compileFailed':
            setStatus(`Failed r${msg.revision}`)
            setLogs(l => [...l, `Error: ${msg.error}`])
            break
          case 'compileCanceled':
            setStatus(`Canceled r${msg.revision}`)
            break
          case 'pong':
            break
          default:
            break
        }
      },
      onClose: () => {
        setStatus('Disconnected')
      },
      onError: () => {
        setStatus('WS error')
      },
    })
    return () => {
      wsService.current?.close()
    }
  }, [projectId])

  const { debounce } = useDebounce()
  const onChange = useCallback(
    (next: string) => {
      setContent(next)
      setRevision(r => r + 1)
      const rev = revision + 1
      debounce(() => {
        wsService.current?.sendMessage({ type: 'docUpdate', path: 'main.tex', content: next, revision: rev })
        // requestCompile will be sent upon ack to avoid race with latest revision
      }, 700)
    },
    [debounce, revision],
  )

  const onSave = useCallback(async () => {
    if (!projectId) return
    setStatus('Saving...')
    const r = await fetch(`/api/projects/${projectId}/files`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        files: [{ path: 'main.tex', content }],
      }),
    })
    if (r.ok) {
      setStatus('Saved — compiling...')
      // Trigger compile via WebSocket after successful save
      wsService.current?.sendMessage({ type: 'requestCompile', path: 'main.tex', revision })
    } else {
      setStatus(`Save failed: ${r.status}`)
    }
  }, [projectId, content])

  const onCompile = useCallback(() => {
    wsService.current?.sendMessage({ type: 'requestCompile', path: 'main.tex', revision })
  }, [revision])

  const pdfSrc = useMemo(() => {
    if (!projectId) return ''
    const ts = Date.now()
    return `/files/${projectId}/output.pdf?rev=${revision}&key=${pdfKey}&t=${ts}`
  }, [projectId, revision, pdfKey])

  return (
    <div style={{ padding: 16, fontFamily: 'system-ui, -apple-system, Segoe UI, Roboto, sans-serif' }}>
      <header style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 8 }}>
        <h1 style={{ margin: 0, fontSize: 18 }}>Live LaTeX Editor</h1>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          {projectId && (
            <a
              href={`/api/projects/${projectId}/download`}
              style={{
                textDecoration: 'none',
                background: '#111827',
                color: 'white',
                padding: '6px 10px',
                borderRadius: 6,
                fontSize: 12,
              }}
            >
              Download ZIP
            </a>
          )}
          <HealthBadge />
        </div>
      </header>
      <p style={{ marginTop: 0, color: '#6b7280', fontSize: 12 }}>
        API Host: {window.location.host} • Status: {status}
      </p>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, alignItems: 'stretch' }}>
        <section style={{ display: 'flex', flexDirection: 'column', minHeight: 400 }}>
          <div style={{ display: 'flex', gap: 8, marginBottom: 8 }}>
            <button onClick={onSave}>Save</button>
            <button onClick={onCompile}>Compile</button>
          </div>
          <textarea
            style={{ width: '100%', height: 500, fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 14 }}
            value={content}
            onChange={e => onChange(e.target.value)}
            spellCheck={false}
          />
          <div style={{ marginTop: 8 }}>
            <h3 style={{ fontSize: 13, margin: '8px 0' }}>Logs</h3>
            <pre style={{ background: '#f3f4f6', padding: 8, borderRadius: 6, height: 120, overflow: 'auto', whiteSpace: 'pre-wrap' }}>
              {logs.join('\n')}
            </pre>
          </div>
        </section>
        <section style={{ minHeight: 400 }}>
          <h3 style={{ fontSize: 13, margin: '0 0 8px 0' }}>PDF Preview</h3>
          {projectId ? (
            <iframe
              key={pdfKey}
              title="PDF Preview"
              src={pdfSrc}
              style={{ width: '100%', height: 650, border: '1px solid #e5e7eb', borderRadius: 6, background: '#fff' }}
            />
          ) : (
            <div style={{ padding: 12, color: '#6b7280' }}>Waiting for project...</div>
          )}
        </section>
      </div>
    </div>
  )
}

export default App
