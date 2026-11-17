import { useEffect, useId, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../api';
import type { ProjectCount } from '../api';

/**
 * ProjectPicker — "which project does this belong to?", asked at the moment
 * work is started rather than as an afterthought.
 *
 * It is deliberately not the same control as ProjectSelector: that one FILTERS
 * an existing view (and so offers "all" and "unscoped only" as scopes), while
 * this one SCOPES a new thing. The distinction matters at one point in
 * particular — the empty-tag aggregate is a filter, never a destination, so it
 * is excluded here. The server labels it `Unscoped` and reports
 * `registered: false`; filtering on the tag itself is the honest check.
 *
 * "New project…" creates the row inline (POST /api/projects) instead of
 * sending someone to another surface to come back later. A non-engineer starts
 * from a prompt, and naming where it belongs must not interrupt that.
 */
export function ProjectPicker({
  value,
  onChange,
  onPending,
  label = 'Project',
  disabled = false,
}: {
  /** The chosen project id. `''` means "no project" — a legitimate answer. */
  value: string;
  onChange: (project: string) => void;
  /**
   * A half-finished "New project…": the name is typed but not created yet.
   * Saving or starting work while this is true used to drop the name silently
   * and file the work under "Unscoped", so the caller blocks on it instead.
   */
  onPending?: (name: string) => void;
  label?: string;
  disabled?: boolean;
}) {
  const fieldId = useId();
  const queryClient = useQueryClient();
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState('');
  const [error, setError] = useState('');

  const { data } = useQuery<ProjectCount[]>({
    queryKey: ['projects'],
    queryFn: () => api.projects(),
  });
  // Real projects only: the empty tag is the unscoped bucket, not a place to
  // put work, and an unregistered tag is offered because work already lives
  // under it even though it has no row yet.
  const projects = (Array.isArray(data) ? data : []).filter((p) => p.project !== '');

  const create = useMutation({
    mutationFn: (name: string) => api.createProject({ name }),
    onSuccess: (created) => {
      setError('');
      setCreating(false);
      setNewName('');
      void queryClient.invalidateQueries({ queryKey: ['projects'] });
      onChange(created.id);
    },
    onError: (e: unknown) => setError(e instanceof Error ? e.message : String(e)),
  });

  const pendingName = creating ? newName.trim() : '';
  useEffect(() => { onPending?.(pendingName); }, [pendingName, onPending]);

  const submitNew = () => {
    const name = newName.trim();
    if (!name) {
      setError('A name is required.');
      return;
    }
    create.mutate(name);
  };

  return (
    <div className="projpick">
      <label className="projpick-label eyebrow" htmlFor={fieldId}>
        {label}
      </label>
      {creating ? (
        <div className="projpick-new">
          <input
            id={fieldId}
            className="input projpick-new-input"
            autoFocus
            value={newName}
            placeholder="Acme launch"
            disabled={disabled || create.isPending}
            aria-label="New project name"
            onChange={(e) => setNewName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault();
                submitNew();
              }
              if (e.key === 'Escape') {
                setCreating(false);
                setError('');
              }
            }}
          />
          <button
            type="button"
            className="btn btn--sm"
            disabled={disabled || create.isPending}
            onClick={submitNew}
          >
            {create.isPending ? 'Creating…' : 'Create'}
          </button>
          <button
            type="button"
            className="btn btn--ghost btn--sm"
            disabled={create.isPending}
            onClick={() => {
              setCreating(false);
              setError('');
            }}
          >
            Cancel
          </button>
          {/* One field stays one field — naming a project must not become a
              form. What it must not do is leave a person thinking a project IS
              a name: the rest is asked once, in the Projects panel, and every
              run and brief under the project then cites it. */}
          <p className="projpick-new-hint">
            A name is enough to start. Add what the project is for, the data it works on, and who
            owns it from the Projects panel — every run and every brief under it will cite them.
          </p>
        </div>
      ) : (
        <select
          id={fieldId}
          className="input projpick-select"
          value={value}
          disabled={disabled}
          onChange={(e) => {
            if (e.target.value === NEW_PROJECT) {
              setCreating(true);
              return;
            }
            onChange(e.target.value);
          }}
        >
          <option value="">No project</option>
          {projects.map((p) => (
            <option key={p.project} value={p.project}>
              {p.name || p.project}
            </option>
          ))}
          <option value={NEW_PROJECT}>New project…</option>
        </select>
      )}
      {error && <div className="projpick-error" role="alert">{error}</div>}
    </div>
  );
}

/** Sentinel option value — never a real project id (ids cannot contain a space). */
const NEW_PROJECT = '__new project__';

/**
 * useHasProjects reports whether any real project exists yet, for the
 * empty-state hints that invite someone to start one. Loading counts as
 * "yes" so the hint never flashes before the answer is known.
 */
export function useHasProjects(): boolean {
  const { data, isLoading } = useQuery<ProjectCount[]>({
    queryKey: ['projects'],
    queryFn: () => api.projects(),
  });
  if (isLoading) return true;
  return (Array.isArray(data) ? data : []).some((p) => p.project !== '');
}

/** The one line every surface uses to invite a first project. */
export const NO_PROJECTS_HINT =
  'Group your work into projects — start one from any prompt or recipe.';
