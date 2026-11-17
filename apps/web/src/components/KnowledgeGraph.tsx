import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import type { KnowledgeGraph as KGraph } from '../api';
import { ProjectSelector } from './ProjectSelector';

// Node colors keyed by knowledge kind, read from CSS tokens (SVG needs literals).
const KINDS = ['decision', 'skill'] as const;
type Kind = (typeof KINDS)[number];

function kindColors(): Record<Kind, string> {
  const cs = getComputedStyle(document.documentElement);
  const v = (n: string) => cs.getPropertyValue(n).trim() || 'currentColor';
  return { decision: v('--kg-0'), skill: v('--kg-1') };
}

interface Pt { x: number; y: number }

// Deterministic Fruchterman–Reingold layout. Small graphs (≤ a few hundred
// nodes) settle in a few hundred O(n²) iterations — fine to run synchronously.
function layout(g: KGraph): Record<string, Pt> {
  const n = g.nodes.length;
  const pos: Record<string, Pt> = {};
  if (n === 0) return pos;
  const W = 1000, H = 1000, cx = W / 2, cy = H / 2;
  const k = Math.sqrt((W * H) / n) * 0.7;
  const R = Math.min(W, H) * 0.4;

  g.nodes.forEach((node, i) => {
    const a = (2 * Math.PI * i) / n;
    pos[node.id] = { x: cx + R * Math.cos(a), y: cy + R * Math.sin(a) };
  });

  const iters = 320;
  for (let it = 0; it < iters; it++) {
    const temp = (1 - it / iters) * (W * 0.1);
    const disp: Record<string, Pt> = {};
    for (const node of g.nodes) disp[node.id] = { x: 0, y: 0 };

    // Repulsion (all pairs).
    for (let i = 0; i < n; i++) {
      for (let j = i + 1; j < n; j++) {
        const a = g.nodes[i].id, b = g.nodes[j].id;
        let dx = pos[a].x - pos[b].x, dy = pos[a].y - pos[b].y;
        let d = Math.hypot(dx, dy) || 0.01;
        const f = (k * k) / d;
        dx /= d; dy /= d;
        disp[a].x += dx * f; disp[a].y += dy * f;
        disp[b].x -= dx * f; disp[b].y -= dy * f;
      }
    }
    // Attraction (edges).
    for (const e of g.edges) {
      if (!pos[e.from] || !pos[e.to]) continue;
      let dx = pos[e.from].x - pos[e.to].x, dy = pos[e.from].y - pos[e.to].y;
      let d = Math.hypot(dx, dy) || 0.01;
      const f = (d * d) / k;
      dx /= d; dy /= d;
      disp[e.from].x -= dx * f; disp[e.from].y -= dy * f;
      disp[e.to].x += dx * f; disp[e.to].y += dy * f;
    }
    // Gravity toward center (keeps disconnected components on-screen).
    for (const node of g.nodes) {
      disp[node.id].x += (cx - pos[node.id].x) * 0.02;
      disp[node.id].y += (cy - pos[node.id].y) * 0.02;
    }
    // Apply, capped by temperature.
    for (const node of g.nodes) {
      const dp = disp[node.id];
      const d = Math.hypot(dp.x, dp.y) || 0.01;
      pos[node.id].x += (dp.x / d) * Math.min(d, temp);
      pos[node.id].y += (dp.y / d) * Math.min(d, temp);
    }
  }
  return pos;
}

export function KnowledgeGraph() {
  const [active, setActive] = useState<Record<Kind, boolean>>({ decision: true, skill: true });
  const [project, setProject] = useState<string | null>(null);
  const selected = KINDS.filter((k) => active[k]);
  const { data, isLoading, error, refetch } = useQuery<KGraph>({
    queryKey: ['knowledge-graph', selected.join(','), project],
    queryFn: () => api.knowledgeGraph(selected, project),
    retry: false,
  });
  const [hover, setHover] = useState<string | null>(null);

  const colors = useMemo(() => kindColors(), []);
  const graph: KGraph = data ?? { nodes: [], edges: [], communities: 0 };
  const pos = useMemo(() => layout(graph), [graph]);

  const view = useMemo(() => {
    const ids = graph.nodes.map((n) => n.id);
    if (ids.length === 0) return { minX: 0, minY: 0, w: 1000, h: 1000 };
    const xs = ids.map((i) => pos[i].x), ys = ids.map((i) => pos[i].y);
    const pad = 80;
    const minX = Math.min(...xs) - pad, minY = Math.min(...ys) - pad;
    const w = Math.max(...xs) - minX + pad, h = Math.max(...ys) - minY + pad;
    return { minX, minY, w, h };
  }, [graph, pos]);

  const neighbors = new Set<string>();
  if (hover) {
    for (const e of graph.edges) {
      if (e.from === hover) neighbors.add(e.to);
      if (e.to === hover) neighbors.add(e.from);
    }
  }
  const dim = (id: string) => hover !== null && id !== hover && !neighbors.has(id);
  const counts = graph.counts ?? {};

  return (
    <div>
      <ProjectSelector value={project} onChange={setProject} />
      <div className="kg-legend">
        <div className="kg-filters">
          {KINDS.map((k) => (
            <button
              key={k}
              className={`kg-chip${active[k] ? ' kg-chip--on' : ''}`}
              onClick={() =>
                setActive((a) => {
                  const next = { ...a, [k]: !a[k] };
                  return KINDS.some((x) => next[x]) ? next : a; // keep ≥1 kind on
                })
              }
              aria-pressed={active[k]}
              aria-label={`toggle ${k}`}
            >
              <span className="kg-swatch" style={{ background: colors[k] }} />
              {k}{counts[k] != null ? ` ${counts[k]}` : ''}
            </button>
          ))}
        </div>
        {!isLoading && !error && <span className="mono">{graph.nodes.length} records · {graph.edges.length} connections · {graph.communities} groups</span>}
      </div>
      {isLoading ? <p role="status">Loading knowledge map…</p> : error ? <p role="alert">Could not load the knowledge map. <button className="btn" onClick={() => void refetch()}>Retry knowledge map</button></p> : graph.nodes.length === 0 ? <p className="empty">No skills or decisions match these filters. Change the filters or add a knowledge record.</p> : <div className="kg-canvas">
        <svg viewBox={`${view.minX} ${view.minY} ${view.w} ${view.h}`} width="100%" height="100%" role="img" aria-label="Knowledge map">
          {graph.edges.map((e, i) => {
            const a = pos[e.from], b = pos[e.to];
            if (!a || !b) return null;
            const faded = hover !== null && e.from !== hover && e.to !== hover;
            return (
              <line
                key={i}
                x1={a.x} y1={a.y} x2={b.x} y2={b.y}
                className={`kg-edge kg-edge--${e.kind}${faded ? ' kg-faded' : ''}`}
              />
            );
          })}
          {graph.nodes.map((n) => {
            const p = pos[n.id];
            const radius = 13 + Math.min(n.degree, 8) * 3;
            return (
              <g
                key={n.id}
                transform={`translate(${p.x},${p.y})`}
                className={dim(n.id) ? 'kg-faded' : ''}
                onMouseEnter={() => setHover(n.id)}
                onMouseLeave={() => setHover(null)}
              >
                <circle r={radius} fill={colors[n.kind as Kind] ?? 'currentColor'} className="kg-node" />
                <text x={radius + 4} y={4} className="kg-label">{n.label}</text>
                {n.status && n.status !== 'accepted' && (
                  <text x={radius + 4} y={18} className="kg-sublabel">{n.status}</text>
                )}
              </g>
            );
          })}
        </svg>
      </div>}
    </div>
  );
}
