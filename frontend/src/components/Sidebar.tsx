import React, { useEffect, useState } from 'react';

type Project = {
  id: string;
  name: string;
  lastModified: string;
};

type User = { id: string; name: string } | null;

type SidebarProps = {
  user: User;
  onLogin: (name: string) => void;
  onLogout: () => void;
  onSelectProject: (project: Project) => void;
  activeProjectId: string | null;
  onNewProject: () => void;
  onImportProject: () => void;
  refresh: boolean;
  onProjectDeleted?: (id: string) => void;
};

const Sidebar: React.FC<SidebarProps> = ({ user, onLogin, onLogout, onSelectProject, activeProjectId, onNewProject, onImportProject, refresh, onProjectDeleted }) => {
  const [projects, setProjects] = useState<Project[]>([]);
  const [hasPdf, setHasPdf] = useState<Record<string, boolean>>({});
  const [username, setUsername] = useState('');

  useEffect(() => {
    fetch('/api/projects', { credentials: 'include' })
      .then(res => (res.ok ? res.json() : { projects: [] }))
      .then(data => setProjects(data.projects || []))
      .catch(() => setProjects([]));
  }, [refresh, user]);

  useEffect(() => {
    let cancelled = false;
    async function probePdfs() {
      const results: Record<string, boolean> = {};
      await Promise.all(projects.map(async (p) => {
        try {
          const r = await fetch(`/files/${p.id}/output.pdf`, { method: 'HEAD', credentials: 'include' });
          results[p.id] = r.ok;
        } catch {
          results[p.id] = false;
        }
      }));
      if (!cancelled) setHasPdf(results);
    }
    if (projects.length > 0) probePdfs();
    return () => { cancelled = true };
  }, [projects]);

  const handleDelete = async (id: string) => {
    if (!confirm('Delete this project? This cannot be undone.')) return;
    const r = await fetch(`/api/projects/${id}`, { method: 'DELETE', credentials: 'include' });
    if (r.ok || r.status === 204) {
      setProjects(prev => prev.filter(p => p.id !== id));
      onProjectDeleted?.(id);
    } else {
      alert('Failed to delete project');
    }
  };

  return (
    <aside className="w-64 bg-slate-50 border-r border-slate-200 p-4 flex flex-col shrink-0">
      <div className="mb-4">
        <h1 className="text-xl font-bold text-slate-800">Live LaTeX</h1>
        <p className="text-sm text-slate-500">Real-time Editor</p>
      </div>
      {!user ? (
        <div className="mb-4 flex gap-2">
          <input
            value={username}
            onChange={e => setUsername(e.target.value)}
            className="flex-1 px-2 py-1 border border-slate-300 rounded"
            placeholder="Username"
          />
          <button onClick={() => { onLogin(username); setUsername(''); }} className="px-3 py-1 bg-blue-600 text-white rounded">
            Login
          </button>
        </div>
      ) : (
        <div className="mb-4 flex items-center justify-between">
          <span className="text-sm">Hi, {user.name}</span>
          <button onClick={onLogout} className="text-sm text-blue-600">Logout</button>
        </div>
      )}
      {user && (
        <div className="space-y-2 mb-4">
          <button
            onClick={onNewProject}
            className="w-full bg-blue-600 hover:bg-blue-700 text-white font-bold py-2 px-4 rounded-lg transition-colors duration-200"
          >
            New Project
          </button>
          <button
            onClick={onImportProject}
            className="w-full bg-slate-200 hover:bg-slate-300 text-slate-800 font-bold py-2 px-4 rounded-lg transition-colors duration-200"
          >
            Import Project
          </button>
        </div>
      )}
      {user && (
        <div className="flex-1 overflow-y-auto">
          <h2 className="text-sm font-semibold text-slate-500 uppercase tracking-wider mb-2">Projects</h2>
          <ul className="space-y-1">
            {projects.map(project => (
              <li key={project.id}>
                <div className={`group flex items-center justify-between p-2 rounded-lg transition-colors duration-200 ${activeProjectId === project.id ? 'bg-blue-100 text-blue-700 shadow-sm' : 'hover:bg-slate-200 text-slate-700'}`}>
                  <a href="#"
                     className="flex-1 min-w-0"
                     onClick={(e) => {
                       e.preventDefault();
                       onSelectProject(project);
                     }}>
                    <div className="font-semibold truncate flex items-center gap-2">
                      <span className="inline-block w-2 h-2 rounded-full" style={{ backgroundColor: hasPdf[project.id] ? '#16a34a' : '#94a3b8' }} title={hasPdf[project.id] ? 'PDF available' : 'No PDF yet'} />
                      {project.name}
                    </div>
                    <div className="text-xs text-slate-500">
                      {new Date(project.lastModified).toLocaleDateString()}
                    </div>
                  </a>
                  <div className="opacity-0 group-hover:opacity-100 transition-opacity flex items-center gap-2 ml-2">
                    <a href={`/api/projects/${project.id}/download`} className="text-xs px-2 py-1 rounded bg-slate-200 hover:bg-slate-300">ZIP</a>
                    <button onClick={() => handleDelete(project.id)} className="text-xs px-2 py-1 rounded bg-red-100 text-red-700 hover:bg-red-200">Delete</button>
                  </div>
                </div>
              </li>
            ))}
          </ul>
        </div>
      )}
    </aside>
  );
};

export default Sidebar;
