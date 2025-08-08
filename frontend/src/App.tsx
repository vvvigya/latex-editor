import { useState, useCallback } from 'react';
import Sidebar from './components/Sidebar';
import EditorView from './components/EditorView';
import NewProjectModal from './components/NewProjectModal';
import ImportProjectModal from './components/ImportProjectModal';
import './App.css';

function App() {
  const [activeProjectId, setActiveProjectId] = useState<string | null>(null);
  const [isNewProjectModalOpen, setIsNewProjectModalOpen] = useState(false);
  const [isImportModalOpen, setIsImportModalOpen] = useState(false);
  const [refreshSidebar, setRefreshSidebar] = useState(false);

  const handleCreateProject = useCallback(async (name: string, template: string) => {
    try {
      const response = await fetch('/api/projects', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, template }),
      });
      const newProject = await response.json();
      if (response.ok) {
        setIsNewProjectModalOpen(false);
        setRefreshSidebar(prev => !prev);
        setActiveProjectId(newProject.id);
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
      });
      const newProject = await response.json();
      if (response.ok) {
        setIsImportModalOpen(false);
        setRefreshSidebar(prev => !prev);
        setActiveProjectId(newProject.id);
      } else {
        alert(`Error importing project: ${newProject.error}`);
      }
    } catch (error) {
      console.error('Failed to import project', error);
      alert('Failed to import project');
    }
  }, []);

  return (
    <div className="flex h-screen bg-white text-slate-900">
      <Sidebar
        onSelectProject={setActiveProjectId}
        activeProjectId={activeProjectId}
        onNewProject={() => setIsNewProjectModalOpen(true)}
        onImportProject={() => setIsImportModalOpen(true)}
        refresh={refreshSidebar}
      />
      {activeProjectId ? (
        <EditorView projectId={activeProjectId} />
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
    </div>
  );
}

export default App;
