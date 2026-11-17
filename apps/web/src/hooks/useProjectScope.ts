import { create } from 'zustand';

/**
 * The app-wide project scope, shared by every view that shows the project
 * chips (Runs, Board, Knowledge) so opening a project scopes the whole app
 * instead of only the view you happened to be on.
 *
 * The three states are distinct and deliberate:
 *   null  — "all": no filter, every project including unscoped rows
 *   ''    — "unscoped only": rows that carry no project
 *   'x'   — scoped to project x
 */
interface ProjectScopeStore {
  project: string | null;
  setProject: (p: string | null) => void;
}

export const useProjectScope = create<ProjectScopeStore>((set) => ({
  project: null,
  setProject: (p) => set({ project: p }),
}));
