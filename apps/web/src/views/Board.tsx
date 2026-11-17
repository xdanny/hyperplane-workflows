import { useState, useCallback } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '../api';
import type { Lane, Ticket, TicketStatus, TicketPriority } from '../api';
import { ProjectSelector } from '../components/ProjectSelector';
import { ProjectPicker } from '../components/ProjectPicker';
import { useSelectedRun } from '../hooks/useSelectedRun';
import { useProjectScope } from '../hooks/useProjectScope';

// ── Constants ─────────────────────────────────────────────────────────────────

const COLUMNS: { status: TicketStatus; label: string }[] = [
  { status: 'todo',        label: 'Todo'        },
  { status: 'in_progress', label: 'In Progress' },
  { status: 'review',      label: 'Review'      },
  { status: 'done',        label: 'Done'        },
  { status: 'blocked',     label: 'Blocked'     },
];

const PRIORITY_DOT: Record<TicketPriority, string> = {
  1: 'bd-priority-low',
  2: 'bd-priority-medium',
  3: 'bd-priority-high',
};

const PRIORITY_LABEL: Record<TicketPriority, string> = {
  1: 'Low', 2: 'Medium', 3: 'High',
};

function colClass(status: TicketStatus): string {
  const map: Record<TicketStatus, string> = {
    todo:        's-idle',
    in_progress: 's-live',
    review:      's-block',
    done:        's-pass',
    blocked:     's-fail',
  };
  return map[status] ?? 's-idle';
}

function fmtRelative(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  if (diff < 60_000) return 'just now';
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`;
  return `${Math.floor(diff / 86_400_000)}d ago`;
}

// ── Ticket Card ───────────────────────────────────────────────────────────────

interface TicketCardProps {
  ticket: Ticket;
  onClick: () => void;
}

function TicketCard({ ticket, onClick }: TicketCardProps) {
  const isWorking = ticket.status === 'in_progress';
  return (
    <button
      className={`bd-card${isWorking ? ' bd-card--working' : ''}`}
      onClick={onClick}
      aria-label={`Open ticket: ${ticket.title}`}
    >
      <div className="bd-card-top">
        <span className={`bd-priority-dot ${PRIORITY_DOT[ticket.priority]}`} aria-label={`Priority: ${PRIORITY_LABEL[ticket.priority]}`} />
        <span className="bd-card-title">{ticket.title}</span>
      </div>
      <div className="bd-card-meta">
        {ticket.assignee && (
          <span className="bd-card-assignee">
            <span className="bd-worker-dot" aria-hidden="true" />
            {ticket.assignee}
          </span>
        )}
        {ticket.deps.length > 0 && (
          <span className="bd-card-deps" aria-label={`${ticket.deps.length} dependencies`}>
            ⬡ {ticket.deps.length}
          </span>
        )}
        <span className="bd-card-age">{fmtRelative(ticket.updatedAt)}</span>
      </div>
    </button>
  );
}

// ── Lane Header ───────────────────────────────────────────────────────────────

interface LaneHeaderProps {
  lane: Lane;
  onAddTicket: () => void;
}

function LaneHeader({ lane, onAddTicket }: LaneHeaderProps) {
  const qc = useQueryClient();
  const { setRunId } = useSelectedRun();
  const [dispatchError, setDispatchError] = useState<string | null>(null);
  const [startedRun, setStartedRun] = useState<string | null>(null);
  const [dispatching, setDispatching] = useState(false);

  const openOriginRun = useCallback(() => {
    if (!lane.originRun) return;
    setRunId(lane.originRun);
  }, [lane.originRun, setRunId]);

  const dispatch = useCallback(async () => {
    setDispatching(true);
    setDispatchError(null);
    try {
      const result = await api.startLaneTask(lane.id);
      setStartedRun(result.runId);
      await qc.invalidateQueries({ queryKey: ['lane-tickets', lane.id] });
      await qc.invalidateQueries({ queryKey: ['lanes'] });
    } catch (err) {
      setDispatchError(err instanceof Error ? err.message : 'Could not start the task');
    } finally {
      setDispatching(false);
    }
  }, [lane.id, qc]);

  const total = lane.counts.todo + lane.counts.inProgress + lane.counts.review + lane.counts.done + lane.counts.blocked;

  return (
    <div className="bd-lane-header">
      <div className="bd-lane-name-row">
        <span className="bd-lane-name">{lane.name}</span>
        <span className="bd-lane-total" aria-label={`${total} tickets`}>{total}</span>
      </div>
      {lane.description && (
        <p className="bd-lane-desc">{lane.description}</p>
      )}
      {lane.originRun && (
        <button
          className="bd-lane-origin"
          onClick={openOriginRun}
          aria-label={`Open origin run ${lane.originRun}`}
        >
          ← from run …{lane.originRun.slice(-10)}
        </button>
      )}
      <div className="bd-lane-counts">
        {lane.counts.inProgress > 0 && (
          <span className="chip s-live" style={{ fontSize: 'var(--t-xs)', padding: '2px 6px' }}>
            <span className="dot" />{lane.counts.inProgress} active
          </span>
        )}
        {lane.counts.blocked > 0 && (
          <span className="chip s-fail" style={{ fontSize: 'var(--t-xs)', padding: '2px 6px' }}>
            <span className="dot" />{lane.counts.blocked} blocked
          </span>
        )}
      </div>
      <div className="bd-lane-actions">
        <button
          className="btn btn--ghost btn--sm bd-add-ticket-btn"
          onClick={onAddTicket}
          aria-label={`Add ticket to lane ${lane.name}`}
        >
          + Ticket
        </button>
        <div className="bd-worker-dispatch">
          <button
            className="btn btn--sm bd-run-workers-btn"
            onClick={dispatch}
            disabled={dispatching || lane.counts.inProgress > 0 || lane.counts.todo === 0}
            aria-label={`Run next task in lane ${lane.name}`}
          >
            {dispatching ? 'Starting…' : 'Run next task'}
          </button>
        </div>
      </div>
      {dispatchError && <p role="alert">{dispatchError}</p>}
      {startedRun && <button className="btn btn--ghost btn--sm" onClick={() => setRunId(startedRun)}>Open started run</button>}
      <p className="bd-lane-desc">One task runs at a time. Mark reviewed work Done before starting its dependents.</p>
    </div>
  );
}

// ── Ticket Detail Drawer ──────────────────────────────────────────────────────

interface TicketDetailProps {
  ticket: Ticket;
  allTickets: Ticket[];
  onClose: () => void;
}

function TicketDetail({ ticket, allTickets, onClose }: TicketDetailProps) {
  const qc = useQueryClient();
  const { setRunId } = useSelectedRun();
  const [actionError, setActionError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const updateMutation = useMutation({
    mutationFn: (body: { status?: TicketStatus; assignee?: string; output?: string }) =>
      api.updateTicket(ticket.id, { ...body, expectedUpdatedAt: ticket.updatedAt, expectedRunId: ticket.sdkRunId ?? '' }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['lane-tickets', ticket.laneId] });
      void qc.invalidateQueries({ queryKey: ['lanes'] });
    },
  });

  const setStatus = async (s: TicketStatus) => {
    setSaving(true);
    try { setActionError(null); await updateMutation.mutateAsync({ status: s }); } catch (err) { setActionError(err instanceof Error ? err.message : 'Could not update task'); } finally { setSaving(false); }
  };

  const depsResolved = ticket.deps.map((depId) => allTickets.find((t) => t.id === depId));

  return (
    <div
      className="bd-detail-overlay"
      role="dialog"
      aria-modal="true"
      aria-label={`Ticket: ${ticket.title}`}
      onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}
    >
      <div className="bd-detail">
        <div className="bd-detail-head">
          <div className="bd-detail-head-left">
            <span className={`bd-priority-dot ${PRIORITY_DOT[ticket.priority]}`} />
            <span className="bd-detail-title">{ticket.title}</span>
          </div>
          <button className="btn btn--ghost btn--sm" onClick={onClose} aria-label="Close ticket detail">
            ✕
          </button>
        </div>

        <div className="bd-detail-body">
          {actionError && <p role="alert">{actionError}</p>}
          {ticket.sdkRunId && <div className="bd-detail-row">
            <button className="btn btn--sm" onClick={() => { setRunId(ticket.sdkRunId!); onClose(); }}>Open run</button>
            {ticket.status === 'in_progress' && <button className="btn btn--sm" disabled={saving} onClick={async () => {
              setSaving(true); setActionError(null);
              try { await api.cancelRun(ticket.sdkRunId!); await qc.invalidateQueries({ queryKey: ['lane-tickets', ticket.laneId] }); await qc.invalidateQueries({ queryKey: ['lanes'] }); }
              catch (err) { setActionError(err instanceof Error ? err.message : 'Could not cancel task'); }
              finally { setSaving(false); }
            }}>Cancel task</button>}
          </div>}
          {/* Status chips */}
          <div className="bd-detail-row">
            <span className="bd-detail-label">Status</span>
            <div className="bd-status-chips">
              {COLUMNS.map(({ status, label }) => (
                <button
                  key={status}
                  className={`btn btn--sm${ticket.status === status ? ' btn--primary' : ''}`}
                  onClick={() => void setStatus(status)}
                  disabled={saving || ticket.status === status || Boolean(ticket.sdkRunId && ticket.status === 'in_progress')}
                  aria-label={`Set status to ${label}`}
                  aria-pressed={ticket.status === status}
                >
                  {label}
                </button>
              ))}
            </div>
          </div>

          {ticket.assignee && (
            <div className="bd-detail-row">
              <span className="bd-detail-label">Worker</span>
              <span className="mono" style={{ fontSize: 'var(--t-sm)' }}>{ticket.assignee}</span>
            </div>
          )}

          {ticket.detail && (
            <div className="bd-detail-row bd-detail-row--block">
              <span className="bd-detail-label">Detail</span>
              <p className="bd-detail-text">{ticket.detail}</p>
            </div>
          )}

          {ticket.deps.length > 0 && (
            <div className="bd-detail-row bd-detail-row--block">
              <span className="bd-detail-label">Deps</span>
              <div className="bd-dep-list">
                {depsResolved.map((dep, i) =>
                  dep ? (
                    <span key={dep.id} className={`chip ${colClass(dep.status)}`} style={{ fontSize: 'var(--t-xs)' }}>
                      <span className="dot" />{dep.title}
                    </span>
                  ) : (
                    <span key={ticket.deps[i]} className="tag mono">{ticket.deps[i]}</span>
                  ),
                )}
              </div>
            </div>
          )}

          {ticket.error && (
            <div className="bd-detail-row bd-detail-row--block">
              <span className="bd-detail-label">Error</span>
              <pre className="bd-detail-pre bd-detail-pre--err">{ticket.error}</pre>
            </div>
          )}

          {ticket.output && (
            <div className="bd-detail-row bd-detail-row--block">
              <span className="bd-detail-label">Output</span>
              <pre className="bd-detail-pre">{ticket.output}</pre>
            </div>
          )}

          <div className="bd-detail-meta">
            <span className="faint" style={{ fontSize: 'var(--t-xs)' }}>
              Updated {fmtRelative(ticket.updatedAt)} · Created {fmtRelative(ticket.createdAt)}
            </span>
            <span className="mono faint" style={{ fontSize: 'var(--t-xs)' }}>{ticket.id}</span>
          </div>
        </div>
      </div>
    </div>
  );
}

// ── New Lane Form ─────────────────────────────────────────────────────────────

interface NewLaneFormProps {
  defaultProject: string;
  onClose: () => void;
}

function NewLaneForm({ defaultProject, onClose }: NewLaneFormProps) {
  const qc = useQueryClient();
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [targetRoot, setTargetRoot] = useState('');
  const [project, setProject] = useState(defaultProject);
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState('');

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) { setErr('Name is required.'); return; }
    setSaving(true);
    setErr('');
    try {
      await api.createLane({
        name: name.trim(),
        description: description.trim() || undefined,
        targetRoot: targetRoot.trim() || undefined,
        project: project.trim() || undefined,
      });
      void qc.invalidateQueries({ queryKey: ['lanes'] });
      onClose();
    } catch (ex) {
      setErr(ex instanceof Error ? ex.message : 'Failed to create lane.');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div
      className="bd-detail-overlay"
      role="dialog"
      aria-modal="true"
      aria-label="Create new lane"
      onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}
    >
      <div className="bd-detail bd-form-dialog">
        <div className="bd-detail-head">
          <span className="bd-detail-title">New lane</span>
          <button className="btn btn--ghost btn--sm" onClick={onClose} aria-label="Close new lane form">✕</button>
        </div>
        <form className="bd-form" onSubmit={(e) => void submit(e)}>
          <label className="bd-field">
            <span className="eyebrow">Name</span>
            <input
              className="input"
              placeholder="e.g. Reliability marts"
              value={name}
              onChange={(e) => setName(e.target.value)}
              required
              autoFocus
            />
          </label>
          <label className="bd-field">
            <span className="eyebrow">Description</span>
            <input
              className="input"
              placeholder="What this lane is building, in one line"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          </label>
          <label className="bd-field">
            <span className="eyebrow">Target root</span>
            <input
              className="input"
              placeholder="The folder the work happens in (a repo or workspace path)"
              value={targetRoot}
              onChange={(e) => setTargetRoot(e.target.value)}
            />
          </label>
          {/* The same picker every other surface uses, instead of a free-text
              box that invented a second way to name a project (Q-31). */}
          <div className="bd-field">
            <ProjectPicker value={project} onChange={setProject} disabled={saving} />
          </div>
          {err && <p className="bd-form-error">{err}</p>}
          <div className="bd-form-actions">
            <button type="button" className="btn btn--ghost" onClick={onClose}>Cancel</button>
            <button type="submit" className="btn btn--primary" disabled={saving}>
              {saving ? 'Creating…' : 'Create lane'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

// ── New Ticket Form ───────────────────────────────────────────────────────────

interface NewTicketFormProps {
  laneId: string;
  laneName: string;
  onClose: () => void;
}

function NewTicketForm({ laneId, laneName, onClose }: NewTicketFormProps) {
  const qc = useQueryClient();
  const [title, setTitle] = useState('');
  const [detail, setDetail] = useState('');
  const [priority, setPriority] = useState<TicketPriority>(2);
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState('');

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!title.trim()) { setErr('Title is required.'); return; }
    setSaving(true);
    setErr('');
    try {
      await api.createTicket({ laneId, title: title.trim(), detail: detail.trim() || undefined, priority });
      void qc.invalidateQueries({ queryKey: ['lane-tickets', laneId] });
      void qc.invalidateQueries({ queryKey: ['lanes'] });
      onClose();
    } catch (ex) {
      setErr(ex instanceof Error ? ex.message : 'Failed to create ticket.');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div
      className="bd-detail-overlay"
      role="dialog"
      aria-modal="true"
      aria-label={`Add ticket to ${laneName}`}
      onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}
    >
      <div className="bd-detail bd-form-dialog">
        <div className="bd-detail-head">
          <span className="bd-detail-title">New ticket</span>
          <span className="faint" style={{ fontSize: 'var(--t-xs)', marginLeft: 'var(--s2)' }}>in {laneName}</span>
          <button className="btn btn--ghost btn--sm" style={{ marginLeft: 'auto' }} onClick={onClose} aria-label="Close new ticket form">✕</button>
        </div>
        <form className="bd-form" onSubmit={(e) => void submit(e)}>
          <label className="bd-field">
            <span className="eyebrow">Title</span>
            <input
              className="input"
              placeholder="e.g. Build the daily revenue mart"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              required
              autoFocus
            />
          </label>
          <label className="bd-field">
            <span className="eyebrow">Detail</span>
            <textarea
              className="input"
              placeholder="Anything the worker needs that the title does not say"
              value={detail}
              rows={3}
              onChange={(e) => setDetail(e.target.value)}
              style={{ resize: 'vertical' }}
            />
          </label>
          <div className="bd-field">
            <span className="eyebrow">Priority</span>
            <div className="bd-priority-group">
              {([1, 2, 3] as TicketPriority[]).map((p) => (
                <button
                  key={p}
                  type="button"
                  className={`btn btn--sm${priority === p ? ' btn--primary' : ''}`}
                  onClick={() => setPriority(p)}
                  aria-pressed={priority === p}
                >
                  <span className={`bd-priority-dot ${PRIORITY_DOT[p]}`} aria-hidden="true" />
                  {PRIORITY_LABEL[p]}
                </button>
              ))}
            </div>
          </div>
          {err && <p className="bd-form-error">{err}</p>}
          <div className="bd-form-actions">
            <button type="button" className="btn btn--ghost" onClick={onClose}>Cancel</button>
            <button type="submit" className="btn btn--primary" disabled={saving}>
              {saving ? 'Adding…' : 'Add ticket'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

// ── Lane Row (one swim lane) ──────────────────────────────────────────────────

interface LaneRowProps {
  lane: Lane;
  onSelectTicket: (ticket: Ticket) => void;
}

function LaneRow({ lane, onSelectTicket }: LaneRowProps) {
  const [showNewTicket, setShowNewTicket] = useState(false);

  const { data: tickets } = useQuery<Ticket[]>({
    queryKey: ['lane-tickets', lane.id],
    queryFn: () => api.laneTickets(lane.id),
    refetchInterval: 2500,
    refetchIntervalInBackground: false,
  });

  const safeTickets = Array.isArray(tickets) ? tickets : [];

  return (
    <>
      <div className="bd-swim-lane">
        <LaneHeader lane={lane} onAddTicket={() => setShowNewTicket(true)} />
        <div className="bd-columns">
          {COLUMNS.map(({ status }) => {
            const colTickets = safeTickets.filter((t) => t.status === status);
            return (
              <div key={status} className="bd-column-cell">
                {colTickets.length === 0 ? (
                  <div className="bd-cell-empty" aria-label="No tickets" />
                ) : (
                  colTickets.map((t) => (
                    <TicketCard key={t.id} ticket={t} onClick={() => onSelectTicket(t)} />
                  ))
                )}
              </div>
            );
          })}
        </div>
      </div>

      {showNewTicket && (
        <NewTicketForm
          laneId={lane.id}
          laneName={lane.name}
          onClose={() => setShowNewTicket(false)}
        />
      )}
    </>
  );
}

// ── Board View ────────────────────────────────────────────────────────────────

export function Board() {
  const [showNewLane, setShowNewLane] = useState(false);
  const [selectedTicket, setSelectedTicket] = useState<Ticket | null>(null);
  const [selectedLaneId, setSelectedLaneId] = useState<string | null>(null);
  // Shared with Runs and Knowledge — one project scope for the whole app.
  const { project, setProject } = useProjectScope();

  const { data: lanes, isLoading } = useQuery<Lane[]>({
    queryKey: ['lanes', project],
    queryFn: () => api.lanes(project),
    refetchInterval: 2500,
    refetchIntervalInBackground: false,
  });

  // Fetch all tickets for the lane of the selected ticket (needed for dep resolution)
  const { data: selectedLaneTickets } = useQuery<Ticket[]>({
    queryKey: ['lane-tickets', selectedLaneId],
    queryFn: () => api.laneTickets(selectedLaneId!),
    enabled: !!selectedLaneId,
  });

  const safeLanes = Array.isArray(lanes) ? lanes : [];
  const activeLanes = safeLanes.filter((l) => l.status !== 'archived');

  const handleSelectTicket = useCallback((ticket: Ticket) => {
    setSelectedTicket(ticket);
    setSelectedLaneId(ticket.laneId);
  }, []);

  const totalActive = activeLanes.reduce((sum, l) => sum + l.counts.inProgress, 0);

  return (
    <div className="bd-view">
      {/* ── View header ───────────────────────────────────────────────────── */}
      <div className="bd-view-head">
        <div className="bd-head-left">
          <span className="eyebrow">Board</span>
          <div className="bd-head-row">
            <h1 className="h1">Build Board</h1>
            {totalActive > 0 && (
              <span className="chip s-live" style={{ fontSize: 'var(--t-xs)' }}>
                <span className="dot" />{totalActive} active
              </span>
            )}
          </div>
        </div>
        <button
          className="btn btn--primary"
          onClick={() => setShowNewLane(true)}
          aria-label="Create new lane"
        >
          + New lane
        </button>
      </div>

      <ProjectSelector value={project} onChange={setProject} />

      {/* ── Loading ───────────────────────────────────────────────────────── */}
      {isLoading && (
        <div className="empty"><span>Loading lanes…</span></div>
      )}

      {/* ── Empty state ───────────────────────────────────────────────────── */}
      {!isLoading && activeLanes.length === 0 && (
        <div className="bd-empty">
          <span className="bd-empty-glyph" aria-hidden="true">⬡</span>
          <span className="bd-empty-title">No lanes yet</span>
          {/* The board is domain-general; the copy used to be an app factory's
              (Q-31). A lane holds whatever kind of work someone tracks here. */}
          <p className="bd-empty-body">
            A lane tracks one piece of work — an app, a dataset, a report — from idea to
            shipped, as tickets moving across the columns.<br />
            Create one to start your board.
          </p>
          <button className="btn btn--primary" onClick={() => setShowNewLane(true)}>
            Create first lane
          </button>
        </div>
      )}

      {/* ── Column header + swim lanes (horizontal scroll wrapper) ────────── */}
      {activeLanes.length > 0 && (
        <div className="bd-scroll-wrap">
          {/* ── Column header row ─────────────────────────────────────────── */}
          <div className="bd-grid-header">
            <div className="bd-lane-header-spacer" aria-hidden="true" />
            <div className="bd-col-labels">
              {COLUMNS.map(({ status, label }) => (
                <div key={status} className="bd-col-label">
                  <span className={`bd-col-label-dot dot ${colClass(status)}`} aria-hidden="true" />
                  <span>{label}</span>
                </div>
              ))}
            </div>
          </div>

          {/* ── Swim lanes ──────────────────────────────────────────────────── */}
          <div className="bd-swim-board">
            {activeLanes.map((lane) => (
              <LaneRow
                key={lane.id}
                lane={lane}
                onSelectTicket={handleSelectTicket}
              />
            ))}
          </div>
        </div>
      )}

      {/* ── Modals ────────────────────────────────────────────────────────── */}
      {showNewLane && <NewLaneForm defaultProject={project ?? ''} onClose={() => setShowNewLane(false)} />}

      {selectedTicket && (
        <TicketDetail
          ticket={selectedLaneTickets?.find((ticket) => ticket.id === selectedTicket.id) ?? selectedTicket}
          allTickets={Array.isArray(selectedLaneTickets) ? selectedLaneTickets : []}
          onClose={() => { setSelectedTicket(null); setSelectedLaneId(null); }}
        />
      )}
    </div>
  );
}
