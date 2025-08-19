import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import WebSocketService from '../services/WebSocketService'
import HealthBadge from './HealthBadge'

// Config
const WS_PROTOCOL = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
const WS_BASE = `${WS_PROTOCOL}//${window.location.host}`

type ServerMsg =
  | { type: 'ack'; ackType: string; revision?: number | string; op?: string }
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
  return { debounce }
}

type EditorViewProps = {
  projectId: string;
  projectName: string;
  onProjectRenamed?: (newName: string) => void;
};

const EditorView: React.FC<EditorViewProps> = ({ projectId, projectName, onProjectRenamed }) => {
  const [content, setContent] = useState<string>('')
  const [revision, setRevision] = useState(0)
  const [status, setStatus] = useState<string>('Loading...')
  const [logs, setLogs] = useState<string[]>([])
  const [pdfKey, setPdfKey] = useState(0)
  const [editorWidthPct, setEditorWidthPct] = useState<number>(60)
  const isResizingRef = useRef(false)
  const [showLogs, setShowLogs] = useState<boolean>(true)
  const [zoom, setZoom] = useState<number>(1)
  const [nameEditing, setNameEditing] = useState<boolean>(false)
  const [nameInput, setNameInput] = useState<string>(projectName)

  const wsService = useRef<WebSocketService | null>(null)

  useEffect(() => {
    setContent('');
    setRevision(0);
    setLogs([]);
    setStatus('Loading file...');

    fetch(`/api/projects/${projectId}/files?path=main.tex`, { credentials: 'include' })
      .then(res => res.json())
      .then(data => {
        if (data && typeof data.content === 'string') {
          setContent(data.content)
          setStatus('File loaded')
        }
      })
      .catch(() => setStatus('Error loading file'));
  }, [projectId]);

  useEffect(() => {
    if (!projectId) return

    const url = `${WS_BASE}/ws/projects/${projectId}`
    wsService.current = new WebSocketService(url, {
      onOpen: () => {
        setStatus('Connected')
        const initialRev = revision || 1
        wsService.current?.sendMessage({ type: 'docUpdate', path: 'main.tex', content, revision: initialRev })
      },
      onMessage: (msg: ServerMsg) => {
        switch (msg.type) {
          case 'ack':
            {
              const rv = msg.revision
              if (typeof rv === 'number') setRevision(rv)
              else if (typeof rv === 'string') {
                const n = parseInt(rv, 10)
                if (!Number.isNaN(n)) setRevision(n)
              }
              const compileRev = typeof rv === 'number' ? rv : (typeof rv === 'string' ? parseInt(rv, 10) : undefined)
              const ackType = (msg as { ackType?: string; op?: string }).ackType || (msg as { ackType?: string; op?: string }).op
              if (ackType === 'docUpdate' && typeof compileRev === 'number' && !Number.isNaN(compileRev)) {
                wsService.current?.sendMessage({ type: 'requestCompile', path: 'main.tex', revision: compileRev })
              }
            }
            break
          case 'compileQueued':
            setStatus(`Queued r${msg.revision}`)
            break
          case 'compileStarted':
            setStatus(`Compiling r${msg.revision}...`)
            setLogs(l => [`[${new Date().toLocaleTimeString()}] Compile started...`, ...l]);
            break
          case 'compileProgress':
            setLogs(l => [msg.message, ...l]);
            break
          case 'compileSucceeded':
            setStatus(`Success r${msg.revision}`)
            setLogs(l => [`[${new Date().toLocaleTimeString()}] PDF updated`, ...l]);
            setPdfKey(k => k + 1)
            break
          case 'compileFailed':
            setStatus(`Failed r${msg.revision}`)
            setLogs(l => [`[${new Date().toLocaleTimeString()}] Error: ${msg.error}`, ...l]);
            break
          case 'compileCanceled':
            setStatus(`Canceled r${msg.revision}`)
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
      }, 700)
    },
    [debounce, revision],
  )

  const onUploadFiles = useCallback(async (files: FileList | null) => {
    if (!files || files.length === 0) return
    const textLike = ['text/', 'application/json', 'application/xml']
    setStatus('Uploading files...')
    const uploadBodies: { path: string; content: string }[] = []
    for (const file of Array.from(files)) {
      const isText = textLike.some(prefix => file.type.startsWith(prefix)) || file.name.endsWith('.tex') || file.name.endsWith('.bib') || file.name.endsWith('.sty') || file.name.endsWith('.cls') || file.name.endsWith('.txt') || file.name.endsWith('.csv')
      if (!isText) {
        setLogs(l => [`[${new Date().toLocaleTimeString()}] Skipped non-text file: ${file.name}`, ...l])
        continue
      }
      const text = await file.text()
      uploadBodies.push({ path: `assets/${file.name}`, content: text })
    }
    if (uploadBodies.length === 0) {
      setStatus('No compatible files to upload')
      return
    }
    try {
      const r = await fetch(`/api/projects/${projectId}/files`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ files: uploadBodies }),
        credentials: 'include',
      })
      if (r.ok) {
        setStatus('Files uploaded')
        setLogs(l => [`[${new Date().toLocaleTimeString()}] Uploaded: ${uploadBodies.map(u => u.path).join(', ')}`, ...l])
      } else {
        setStatus(`Upload failed: ${r.status}`)
      }
    } catch {
      setStatus('Upload failed')
    }
  }, [projectId])

  // Resizer handlers
  const onStartResize = useCallback(() => {
    isResizingRef.current = true
    document.body.style.cursor = 'col-resize'
  }, [])
  const onStopResize = useCallback(() => {
    isResizingRef.current = false
    document.body.style.cursor = ''
  }, [])
  const onResize = useCallback((e: MouseEvent) => {
    if (!isResizingRef.current) return
    const container = document.getElementById('editor-preview-container')
    if (!container) return
    const rect = container.getBoundingClientRect()
    const pct = Math.min(80, Math.max(20, ((e.clientX - rect.left) / rect.width) * 100))
    setEditorWidthPct(pct)
  }, [])
  useEffect(() => {
    const move = (e: MouseEvent) => onResize(e)
    const up = () => onStopResize()
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
    return () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
    }
  }, [onResize, onStopResize])

  useEffect(() => {
    setNameInput(projectName)
  }, [projectName])

  const onSave = useCallback(async () => {
    if (!projectId) return
    setStatus('Saving...')
    const r = await fetch(`/api/projects/${projectId}/files`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        files: [{ path: 'main.tex', content }],
      }),
      credentials: 'include',
    })
    if (r.ok) {
      setStatus('Saved, compiling...')
      wsService.current?.sendMessage({ type: 'requestCompile', path: 'main.tex', revision })
    } else {
      setStatus(`Save failed: ${r.status}`)
    }
  }, [projectId, content, revision])

  const onCompile = useCallback(() => {
    wsService.current?.sendMessage({ type: 'requestCompile', path: 'main.tex', revision })
  }, [revision])

  const pdfSrc = useMemo(() => {
    if (!projectId) return ''
    return `/files/${projectId}/output.pdf?key=${pdfKey}&t=${Date.now()}`
  }, [projectId, pdfKey])

  const onRenameSubmit = useCallback(async () => {
    const newName = nameInput.trim()
    if (!newName || newName === projectName) {
      setNameEditing(false)
      return
    }
    try {
      const res = await fetch(`/api/projects/${projectId}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: newName }),
        credentials: 'include',
      })
      if (res.ok) {
        setStatus('Renamed project')
        onProjectRenamed?.(newName)
      } else {
        setStatus('Rename not supported by server')
        setNameInput(projectName)
      }
    } catch {
      setStatus('Rename failed')
      setNameInput(projectName)
    } finally {
      setNameEditing(false)
    }
  }, [nameInput, projectId, projectName, onProjectRenamed])

  return (
    <main className="flex-1 flex flex-col bg-slate-100">
      <header className="flex items-center justify-between p-4 bg-white border-b border-slate-200">
        <div className="flex items-center gap-3">
          <h2 className="text-lg font-semibold text-slate-800">Project:</h2>
          {nameEditing ? (
            <input
              autoFocus
              value={nameInput}
              onChange={(e) => setNameInput(e.target.value)}
              onBlur={onRenameSubmit}
              onKeyDown={(e) => {
                if (e.key === 'Enter') onRenameSubmit()
                if (e.key === 'Escape') { setNameEditing(false); setNameInput(projectName) }
              }}
              className="px-2 py-1 border border-slate-300 rounded focus:outline-none focus:ring-blue-500 focus:border-blue-500 text-slate-800"
            />
          ) : (
            <button
              className="text-lg font-semibold text-blue-700 hover:underline"
              title="Click to rename"
              onClick={() => setNameEditing(true)}
            >
              {nameInput}
            </button>
          )}
          <span className="text-xs text-slate-500">({projectId})</span>
        </div>
        <div className="flex items-center gap-4">
          <span className="text-sm text-slate-600">Status: {status}</span>
          <a href={`/api/projects/${projectId}/download`} className="text-sm font-medium text-blue-600 hover:underline">
            Download ZIP
          </a>
          <HealthBadge />
        </div>
      </header>
      <div id="editor-preview-container" className="flex-1 flex flex-row gap-4 p-4 overflow-hidden">
        <section className="flex flex-col bg-white rounded-lg shadow-sm border border-slate-200 overflow-hidden" style={{ width: `${editorWidthPct}%` }}>
          <div className="flex items-center justify-between p-2 border-b border-slate-200">
            <h3 className="text-sm font-semibold text-slate-600 px-2">main.tex</h3>
            <div className="flex gap-2">
              <button onClick={onSave} className="px-3 py-1 bg-blue-600 text-white text-sm rounded-md hover:bg-blue-700 transition-colors">Save</button>
              <button onClick={onCompile} className="px-3 py-1 bg-slate-200 text-sm rounded-md hover:bg-slate-300 transition-colors">Recompile</button>
              <label className="px-3 py-1 bg-slate-100 text-sm rounded-md border border-slate-300 cursor-pointer hover:bg-slate-200 transition-colors">
                Upload files
                <input type="file" multiple className="hidden" onChange={(e) => onUploadFiles(e.target.files)} accept=".tex,.bib,.sty,.cls,.txt,.csv,.json,.xml" />
              </label>
              <button onClick={() => setShowLogs(s => !s)} className="px-3 py-1 bg-slate-100 text-sm rounded-md border border-slate-300 hover:bg-slate-200 transition-colors">
                {showLogs ? 'Hide Logs' : 'Show Logs'}
              </button>
            </div>
          </div>
          <textarea
            className="flex-1 w-full p-4 font-mono text-sm resize-none focus:outline-none"
            value={content}
            onChange={e => onChange(e.target.value)}
            spellCheck={false}
          />
          {showLogs && (
            <div className="border-t border-slate-200">
              <h3 className="text-sm font-semibold text-slate-600 p-2">Logs</h3>
              <pre className="h-32 p-2 bg-slate-50 text-xs overflow-y-auto">
                {logs.join('\n')}
              </pre>
            </div>
          )}
        </section>
        <div
          className="w-1 bg-slate-200 hover:bg-slate-300 cursor-col-resize rounded self-stretch"
          onMouseDown={onStartResize}
          title="Drag to resize"
        />
        <section className="flex flex-col bg-white rounded-lg shadow-sm border border-slate-200 overflow-hidden" style={{ width: `${100 - editorWidthPct}%` }}>
          <h3 className="text-sm font-semibold text-slate-600 p-2 border-b border-slate-200">PDF Preview</h3>
          <div className="flex items-center gap-2 px-2 pb-2 text-sm text-slate-600">
            <span>Zoom</span>
            <button className="px-2 py-0.5 border rounded" onClick={() => setZoom(z => Math.max(0.5, +(z - 0.1).toFixed(2)))}>-</button>
            <span className="w-10 text-center">{Math.round(zoom * 100)}%</span>
            <button className="px-2 py-0.5 border rounded" onClick={() => setZoom(z => Math.min(2, +(z + 0.1).toFixed(2)))}>+</button>
            <button className="px-2 py-0.5 border rounded" onClick={() => setZoom(1)}>Reset</button>
          </div>
          <div className="flex-1 overflow-auto">
            <div style={{ transform: `scale(${zoom})`, transformOrigin: 'top left' }}>
              <iframe
                key={pdfKey}
                title="PDF Preview"
                src={pdfSrc}
                className="w-full h-[calc(100vh-260px)]"
              />
            </div>
          </div>
        </section>
      </div>
    </main>
  );
};

export default EditorView;
