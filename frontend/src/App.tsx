import { useState, useCallback, useEffect } from 'react';
import Sidebar from './components/Sidebar';
import EditorView from './components/EditorView';
import NewProjectModal from './components/NewProjectModal';
import ImportProjectModal from './components/ImportProjectModal';
import './App.css';

type ActiveProject = { id: string; name: string } | null;
type User = { id: string; name: string } | null;

function App() {
  const [activeProject, setActiveProject] = useState<ActiveProject>(null);
  const [isNewProjectModalOpen, setIsNewProjectModalOpen] = useState(false);
  const [isImportModalOpen, setIsImportModalOpen] = useState(false);
  const [refreshSidebar, setRefreshSidebar] = useState(false);
  const [apiVersion, setApiVersion] = useState('');
  const [user, setUser] = useState<User>(null);

  const handleLogin = useCallback(async (name: string) => {
    try {
      const res = await fetch('/api/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name }),
        credentials: 'include',
      });
      if (res.ok) {
        const u = await res.json();
        setUser(u);
        setRefreshSidebar(prev => !prev);
      }
    } catch (e) {
      console.error('login failed', e);
    }
  }, []);

  const handleLogout = useCallback(async () => {
    try {
      await fetch('/api/logout', { method: 'POST', credentials: 'include' });
    } catch {
      /* ignore */
    }
    setUser(null);
    setRefreshSidebar(prev => !prev);
    setActiveProject(null);
  }, []);

  const handleCreateProject = useCallback(async (name: string, template: string) => {
    try {
      const response = await fetch('/api/projects', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, template }),
        credentials: 'include',
      });
      const newProject = await response.json();
      if (response.ok) {
        setIsNewProjectModalOpen(false);
        setRefreshSidebar(prev => !prev);
        setActiveProject({ id: newProject.id, name: newProject.name || 'Untitled' });
      } else {
        alert(`Error creating project: ${newProject.error}`);
      }
    } catch (error) {
      console.error('Failed to create project', error);
      alert('Failed to create project');
    }
  }, []);

  const handleImportProject = useCallback(async (file: File) => {
    const formData = new FormData();
    formData.append('file', file);
    try {
      const response = await fetch('/api/projects/import', {
        method: 'POST',
        body: formData,
        credentials: 'include',
      });
      const newProject = await response.json();
      if (response.ok) {
        setIsImportModalOpen(false);
        setRefreshSidebar(prev => !prev);
        setActiveProject({ id: newProject.id, name: newProject.name || 'Imported project' });
      } else {
        alert(`Error importing project: ${newProject.error}`);
      }
    } catch (error) {
      console.error('Failed to import project', error);
      alert('Failed to import project');
    }
  }, []);

  useEffect(() => {
    fetch('/api/version', { credentials: 'include' })
      .then((r) => r.json())
      .then((data) => setApiVersion(data.api || ''))
      .catch(() => setApiVersion(''));
    fetch('/api/me', { credentials: 'include' })
      .then(r => (r.ok ? r.json() : null))
      .then(u => setUser(u))
      .catch(() => setUser(null));
  }, []);

  return (
    <div className="flex h-screen bg-white text-slate-900">
      <Sidebar
        user={user}
        onLogin={handleLogin}
        onLogout={handleLogout}
        onSelectProject={(p) => setActiveProject({ id: p.id, name: p.name })}
        activeProjectId={activeProject?.id || null}
        onNewProject={() => setIsNewProjectModalOpen(true)}
        onImportProject={() => setIsImportModalOpen(true)}
        refresh={refreshSidebar}
        onProjectDeleted={(id) => {
          if (activeProject?.id === id) setActiveProject(null);
        }}
      />
      {activeProject ? (
        <EditorView
          projectId={activeProject.id}
          projectName={activeProject.name}
          onProjectRenamed={(newName: string) => {
            setActiveProject(prev => (prev ? { ...prev, name: newName } : prev));
            setRefreshSidebar(prev => !prev);
          }}
        />
      ) : (
        <div className="flex-1 flex items-center justify-center text-slate-500">
          Select a project or create a new one to start
        </div>
      )}
      <NewProjectModal
        isOpen={isNewProjectModalOpen}
        onClose={() => setIsNewProjectModalOpen(false)}
        onCreate={handleCreateProject}
      />
      <ImportProjectModal
        isOpen={isImportModalOpen}
        onClose={() => setIsImportModalOpen(false)}
        onImport={handleImportProject}
      />
      {apiVersion && (
        <div className="absolute bottom-2 right-2 text-xs text-slate-400">
          API v{apiVersion}
        </div>
      )}
    </div>
  );
}

export default App;
