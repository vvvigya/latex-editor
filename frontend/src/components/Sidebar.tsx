import React, { useEffect, useState } from 'react';

type Project = {
  id: string;
  name: string;
  lastModified: string;
};

type SidebarProps = {
  onSelectProject: (id: string) => void;
  activeProjectId: string | null;
  onNewProject: () => void;
  onImportProject: () => void;
  refresh: boolean;
};

const Sidebar: React.FC<SidebarProps> = ({ onSelectProject, activeProjectId, onNewProject, onImportProject, refresh }) => {
  const [projects, setProjects] = useState<Project[]>([]);

  useEffect(() => {
    fetch('/api/projects')
      .then(res => res.json())
      .then(data => setProjects(data.projects || []))
      .catch(console.error);
  }, [refresh]);

  return (
    <aside className="w-64 bg-slate-50 border-r border-slate-200 p-4 flex flex-col shrink-0">
      <div className="mb-4">
        <h1 className="text-xl font-bold text-slate-800">Live LaTeX</h1>
        <p className="text-sm text-slate-500">Real-time Editor</p>
      </div>
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
      <div className="flex-1 overflow-y-auto">
        <h2 className="text-sm font-semibold text-slate-500 uppercase tracking-wider mb-2">Projects</h2>
        <ul className="space-y-1">
          {projects.map(project => (
            <li key={project.id}>
              <a href="#"
                 className={`block p-2 rounded-lg transition-colors duration-200 ${activeProjectId === project.id ? 'bg-blue-100 text-blue-700 shadow-sm' : 'hover:bg-slate-200 text-slate-700'}`}
                 onClick={(e) => {
                   e.preventDefault();
                   onSelectProject(project.id);
                 }}>
                <div className="font-semibold">{project.name}</div>
                <div className="text-xs text-slate-500">
                  {new Date(project.lastModified).toLocaleDateString()}
                </div>
              </a>
            </li>
          ))}
        </ul>
      </div>
    </aside>
  );
};

export default Sidebar;
