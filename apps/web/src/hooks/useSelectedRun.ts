import { useCallback } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

// A run is selected by its URL, so reloads and shared links keep the same run open.
export function useSelectedRun() {
  const navigate = useNavigate();
  const { runId = null } = useParams();
  const setRunId = useCallback((id: string | null) => {
    navigate(id ? `/runs/${encodeURIComponent(id)}` : '/runs');
  }, [navigate]);
  return { runId, setRunId, openRun: setRunId };
}
