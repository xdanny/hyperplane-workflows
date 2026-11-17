import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import type { ProjectCount } from '../api';
import { ProjectsPanel, countedUnits } from './ProjectsPanel';
import { ReadStatus } from './ReadStatus';
import { NO_PROJECTS_HINT } from './ProjectPicker';

/**
 * ProjectSelector — chips to scope a view to a project, and the way into the
 * Projects surface.
 *
 * `value === null` means "all" (no filter — every project, including
 * unscoped rows). `value === ''` means "unscoped only" (rows with no
 * project). `value === '<name>'` scopes to that project. These are distinct
 * states: the "all" and "unscoped" chips emit different values and highlight
 * independently, so callers can tell "show everything" apart from "show only
 * the rows that have no project" instead of collapsing both to no filter.
 *
 * The chips row expands into the Projects panel rather than the app growing a
 * nav destination: same place you pick a scope, one click deeper for the
 * project itself. The chips themselves stay hidden until there is more than
 * one project (no need to scope a single one), but the "Projects" toggle is
 * always there — otherwise there would be nowhere to create the first one.
 *
 * That toggle used to be a chip indistinguishable from the scope chips beside
 * it, which is how the whole Projects surface managed to stay invisible. It is
 * now a labelled disclosure button carrying the project count and a caret, and
 * when there are no projects at all it says what one is for instead of sitting
 * there as a word.
 *
 * `defaultOpen` is how the nav's Projects entry lands here with the panel
 * already showing: still one surface, still inside Runs, just reachable from
 * the rail instead of only by knowing it is here.
 */
export function ProjectSelector({
  value,
  onChange,
  defaultOpen = false,
  onOpenChange,
}: {
  value: string | null;
  onChange: (p: string | null) => void;
  /** Start expanded — set by the ?projects=1 entry in the nav. */
  defaultOpen?: boolean;
  /** Told whenever the panel opens or closes, so the URL can stay honest. */
  onOpenChange?: (open: boolean) => void;
}) {
  const [open, setOpen] = useState(defaultOpen);
  // Follow the URL when it changes under us (clicking Projects in the nav while
  // already on Runs). Same value in, same value out — no loop with onOpenChange.
  useEffect(() => setOpen(defaultOpen), [defaultOpen]);

  const setOpenAndTell = (next: boolean) => {
    setOpen(next);
    onOpenChange?.(next);
  };
  const projectsQuery = useQuery<ProjectCount[]>({ queryKey: ['projects'], queryFn: () => api.projects() });
  const projects = Array.isArray(projectsQuery.data) ? projectsQuery.data : [];
  // These chips scope the RUNS list, and the number on them counts runs. It
  // used to be `count` — runs plus lanes plus decisions summed into a bare
  // number with no unit, so "all 72" named neither what was counted nor what
  // picking the chip would show.
  const total = projects.reduce((a, p) => a + p.runs, 0);
  // The empty tag is the unscoped bucket, not a project — it must not count
  // towards "you have projects", or the invitation to make one never appears.
  const real = projects.filter((p) => p.project !== '');

  return (
    <div className="proj-scope">
      <div className="proj-bar" role="group" aria-label="Project scope">
        <button
          className={`btn btn--sm proj-toggle${open ? ' proj-toggle--on' : ''}`}
          onClick={() => setOpenAndTell(!open)}
          aria-expanded={open}
        >
          <span className="proj-toggle-caret" aria-hidden="true">{open ? '▾' : '▸'}</span>
          Projects
          {projectsQuery.data !== undefined && <span className="mono proj-toggle-count">{real.length}</span>}
        </button>
        {projects.length > 1 && (
          <>
            <button
              className={`proj-chip${value === null ? ' proj-chip--on' : ''}`}
              onClick={() => onChange(null)}
              aria-pressed={value === null}
            >
              all <span className="mono">{total}</span> {total === 1 ? 'run' : 'runs'}
            </button>
            {projects.map((p) => (
              <button
                key={p.project || '_unscoped'}
                className={`proj-chip${value === p.project ? ' proj-chip--on' : ''}`}
                onClick={() => onChange(p.project)}
                aria-pressed={value === p.project}
                title={`${countedUnits(p)} under ${p.name || p.project || 'no project'}`}
              >
                {/* The unscoped bucket arrives named from the server; the
                    fallbacks are for a tag that has no row yet. */}
                {p.name || p.project || 'Unscoped'} <span className="mono">{p.runs}</span>{' '}
                {p.runs === 1 ? 'run' : 'runs'}
              </button>
            ))}
          </>
        )}
      </div>
      {!open && <ReadStatus query={projectsQuery} label="projects" />}
      {!open && projectsQuery.isSuccess && real.length === 0 && (
        <button className="proj-invite" onClick={() => setOpenAndTell(true)}>
          {NO_PROJECTS_HINT}
        </button>
      )}
      {open && (
        <ProjectsPanel
          scoped={value}
          // Opening a project scopes the whole app to it; '' from the panel's
          // "All projects" means "leave the detail", which is the no-filter
          // state rather than the unscoped-only one.
          onOpen={(project) => onChange(project === '' ? null : project)}
          onScopeUnscoped={() => onChange('')}
          onClose={() => setOpenAndTell(false)}
        />
      )}
    </div>
  );
}
