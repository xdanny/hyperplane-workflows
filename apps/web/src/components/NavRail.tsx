import { Link, useLocation } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import type { InboxItem, Health } from '../api';

/**
 * The primary nav.
 *
 * Two things it says on purpose:
 *   · Projects is a destination with its own path (`/projects`, and
 *     `/projects/<tag>` for one of them). It renders the Runs view with its
 *     projects rail open — the surface has always lived there — but it is a URL
 *     now, so a project can be linked to instead of described.
 *   · Console is LAST and tagged "advanced". It is a raw terminal onto the
 *     orchestrator; second place in the rail read as "use me next".
 */
interface NavEntry {
  to: string;
  /** Query string that distinguishes two entries sharing a path. */
  search?: string;
  ico: string;
  label: string;
  /** A quiet qualifier chip — "this is not the ordinary path". */
  tag?: string;
  /** Fuller sentence for the hover, when the label alone under-explains. */
  hint?: string;
}

const NAV: NavEntry[] = [
  { to: '/',          ico: '⌂', label: 'Home' },
  { to: '/runs',      ico: '↻', label: 'Runs' },
  {
    to: '/projects',
    ico: '▤',
    label: 'Projects',
    hint: 'Your projects — the runs, deliveries and decisions grouped under each one.',
  },
  { to: '/workflows', ico: '⌗', label: 'Workflows' },
  { to: '/inbox',     ico: '◆', label: 'Inbox' },
  { to: '/knowledge', ico: '✦', label: 'Knowledge' },
  { to: '/board',     ico: '⬡', label: 'Board' },
  {
    to: '/console',
    ico: '❯',
    label: 'Console',
    tag: 'advanced',
    hint: 'A raw terminal onto the orchestrator. Nothing in the rest of the app needs it.',
  },
];

/** The router location, without importing the type name it happens to use. */
type RouteLocation = ReturnType<typeof useLocation>;

/** Whether the projects rail is the thing currently on screen. The legacy
 *  `/runs?projects=1` link still lands there, so it still lights Projects. */
function projectsRailOpen(location: RouteLocation): boolean {
  if (location.pathname === '/projects' || location.pathname.startsWith('/projects/')) return true;
  return (
    location.pathname === '/runs' &&
    new URLSearchParams(location.search).get('projects') === '1'
  );
}

/** Which single entry is "where you are". */
function isActive(entry: NavEntry, location: RouteLocation): boolean {
  if (entry.to === '/') return location.pathname === '/';
  if (entry.to === '/projects') return projectsRailOpen(location);
  if (entry.to === '/runs') {
    // `/runs/<id>` is a run detail page — still Runs, and never the Projects
    // rail, which has its own path.
    const onRuns = location.pathname === '/runs' || location.pathname.startsWith('/runs/');
    return onRuns && !projectsRailOpen(location);
  }
  return location.pathname.startsWith(entry.to);
}

export function NavRail() {
  const location = useLocation();
  const { data: inbox } = useQuery<InboxItem[]>({
    queryKey: ['inbox'], queryFn: () => api.inbox(), refetchInterval: 10000,
  });
  const { data: health, isError } = useQuery<Health>({
    queryKey: ['health'], queryFn: () => api.health(), refetchInterval: 5000,
  });

  const pending = Array.isArray(inbox) ? inbox.length : 0;
  const status = isError ? 'unreachable' : !health ? 'checking' : health.ok ? 'online' : 'degraded';

  return (
    <aside className="sidebar">
      <div className="wordmark">
        <span className="glyph">⌗</span>
        <span className="name">Hyperplane</span>
      </div>

      <nav className="nav" aria-label="Primary">
        {NAV.map((entry) => {
          const { to, search, ico, label, tag, hint } = entry;
          const active = isActive(entry, location);
          return (
            <Link
              key={`${to}${search ?? ''}`}
              to={`${to}${search ?? ''}`}
              className={`nav-item${active ? ' active' : ''}`}
              aria-current={active ? 'page' : undefined}
              title={hint}
            >
              <span className="ico" aria-hidden="true">{ico}</span>
              <span>{label}</span>
              {tag && <span className="nav-tag">{tag}</span>}
              {to === '/inbox' && pending > 0 && <span className="badge">{pending > 9 ? '9+' : pending}</span>}
            </Link>
          );
        })}
      </nav>

      <div className="sidebar-foot">
        <div className={`health ${status}`} title={`API: ${status}`}>
          <span className="dot" />
          <span>{status}</span>
        </div>
      </div>
    </aside>
  );
}
