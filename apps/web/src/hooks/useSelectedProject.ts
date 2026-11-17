import { useCallback } from 'react';
import { useLocation, useNavigate, useParams } from 'react-router-dom';

/**
 * WHICH PROJECT IS OPEN IS A PROPERTY OF THE URL.
 *
 * The Projects surface is the projects rail inside Runs, and it was reachable
 * only as `/runs?projects=1` with the open project held in a module-level
 * store. Two consequences, both reported: typing `/projects` landed on Home,
 * and there was no link to hand anyone — "who owns this?" cannot be answered
 * by pasting a URL when every screen has the same URL (P-13).
 *
 * `/projects` is the panel; `/projects/<tag>` is one project. This mirrors
 * useSelectedRun exactly, on purpose: the same shape means Back/Forward and a
 * reload behave the same way in both places. Nothing here navigates on its own
 * — every function below runs from a click handler.
 */
export function useSelectedProject() {
  const navigate = useNavigate();
  const params = useParams();
  const location = useLocation();

  /** True on `/projects` and `/projects/<tag>` alike. */
  const onProjectsRoute =
    location.pathname === '/projects' || location.pathname.startsWith('/projects/');

  /** The project on screen, or null on the panel route. */
  const projectId = params.projectId ?? null;

  /** Open one project, or (with null) the panel listing all of them. */
  const openProject = useCallback(
    (id: string | null) => {
      navigate(id ? `/projects/${encodeURIComponent(id)}` : '/projects');
    },
    [navigate],
  );

  return { onProjectsRoute, projectId, openProject };
}
