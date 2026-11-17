import { useEffect, useRef, useState } from 'react';
import '@xterm/xterm/css/xterm.css';
import { terminalWsUrl } from '../api';

type Kind = 'orchestrator' | 'shell';
type Status = 'connecting' | 'live' | 'closed';
export type TermEngine = 'xterm' | 'ghostty';

// xterm renders to a canvas and needs literal colour strings, so we read the
// terminal tokens (defined in theme.css) at runtime rather than hardcoding them.
function readTheme() {
  const cs = getComputedStyle(document.documentElement);
  const v = (name: string) => cs.getPropertyValue(name).trim();
  return {
    background: v('--term-bg'),
    foreground: v('--term-fg'),
    cursor: v('--term-accent'),
    cursorAccent: v('--term-bg'),
    selectionBackground: v('--term-sel'),
    black: v('--term-bg'),
    brightBlack: v('--term-fg-mute'),
    blue: v('--term-accent'),
    brightBlue: v('--term-accent-2'),
    green: v('--term-green'),
    brightGreen: v('--term-green-2'),
    red: v('--term-red'),
    brightRed: v('--term-red-2'),
    yellow: v('--term-yellow'),
    brightYellow: v('--term-yellow-2'),
    white: v('--term-fg'),
    brightWhite: v('--term-white'),
  };
}

// loadEngine dynamically imports the chosen terminal engine. ghostty-web exposes
// the same Terminal/FitAddon API as xterm but needs a one-time async init() to
// load its WASM. Returns the actual engine loaded (ghostty falls back to xterm
// on any failure, since ghostty-web is v0.x and WASM may not load everywhere).
async function loadEngine(
  engine: TermEngine,
): Promise<{ Terminal: any; FitAddon: any; used: TermEngine }> {
  if (engine === 'ghostty') {
    try {
      const g: any = await import('ghostty-web');
      await g.init();
      return { Terminal: g.Terminal, FitAddon: g.FitAddon, used: 'ghostty' };
    } catch (err) {
      console.warn('[Console] ghostty-web failed to load; falling back to xterm', err);
    }
  }
  const x: any = await import('@xterm/xterm');
  const f: any = await import('@xterm/addon-fit');
  return { Terminal: x.Terminal, FitAddon: f.FitAddon, used: 'xterm' };
}

/**
 * TerminalPane mounts one terminal (xterm or ghostty-web) bound to a PTY over a
 * WebSocket. stdin is sent as Binary frames; resize as a Text JSON control frame;
 * PTY output arrives as Binary. The pane stays mounted (hidden) when inactive so
 * the session survives tab switches. Both engines share the same /api/terminal/ws.
 */
export function TerminalPane({ kind, active, engine }: { kind: Kind; active: boolean; engine: TermEngine }) {
  const hostRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<any>(null);
  const fitRef = useRef<any>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const [status, setStatus] = useState<Status>('connecting');
  const [used, setUsed] = useState<TermEngine | null>(null);
  const [generation, setGeneration] = useState(0); // bump to reconnect

  // Create the terminal once per engine choice.
  useEffect(() => {
    let cancelled = false;
    let term: any = null;
    let ro: ResizeObserver | null = null;

    const onResize = () => {
      try {
        fitRef.current?.fit();
      } catch {
        /* ignore */
      }
      sendResize();
    };

    loadEngine(engine).then(({ Terminal, FitAddon, used }) => {
      if (cancelled || !hostRef.current) return;
      term = new Terminal({
        theme: readTheme(),
        fontFamily: 'ui-monospace, "SF Mono", "JetBrains Mono", Menlo, monospace',
        fontSize: 13,
        lineHeight: 1.25,
        cursorBlink: true,
        scrollback: 10000,
        allowProposedApi: true,
      });
      const fit = new FitAddon();
      term.loadAddon(fit);
      term.open(hostRef.current);
      try {
        fit.fit();
      } catch {
        /* element not yet laid out */
      }
      termRef.current = term;
      fitRef.current = fit;
      setUsed(used);
      setGeneration((g) => g + 1); // trigger the ws connect now that the term exists

      ro = new ResizeObserver(onResize);
      ro.observe(hostRef.current);
      window.addEventListener('resize', onResize);
    });

    return () => {
      cancelled = true;
      ro?.disconnect();
      window.removeEventListener('resize', onResize);
      wsRef.current?.close();
      try {
        term?.dispose();
      } catch {
        /* ignore */
      }
      termRef.current = null;
      fitRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [engine]);

  function sendResize() {
    const term = termRef.current;
    const ws = wsRef.current;
    if (!term || !ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({ resize: { cols: term.cols, rows: term.rows } }));
  }

  // (Re)connect the WebSocket whenever generation changes (and the term exists).
  useEffect(() => {
    const term = termRef.current;
    if (!term) return;
    setStatus('connecting');

    const ws = new WebSocket(terminalWsUrl(kind, term.cols, term.rows));
    ws.binaryType = 'arraybuffer';
    wsRef.current = ws;
    const enc = new TextEncoder();

    ws.onopen = () => {
      setStatus('live');
      sendResize();
      term.focus();
    };
    ws.onmessage = (ev: MessageEvent) => {
      if (typeof ev.data === 'string') term.write(ev.data);
      else term.write(new Uint8Array(ev.data as ArrayBuffer));
    };
    ws.onclose = () => {
      setStatus('closed');
      term.write('\r\n\x1b[2m── session closed ──\x1b[0m\r\n');
    };

    const dataSub = term.onData((d: string) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(enc.encode(d));
    });

    return () => {
      dataSub.dispose();
      ws.close();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [generation, kind]);

  // Refit when this pane becomes visible.
  useEffect(() => {
    if (!active) return;
    const id = window.setTimeout(() => {
      try {
        fitRef.current?.fit();
      } catch {
        /* ignore */
      }
      sendResize();
      termRef.current?.focus();
    }, 30);
    return () => window.clearTimeout(id);
  }, [active]);

  return (
    <div className="term-pane" style={{ display: active ? 'flex' : 'none' }}>
      <div className="term-bar">
        <span className={`term-status ${status}`}>
          <span className="dot" />
          {status === 'live' ? 'connected' : status === 'connecting' ? 'connecting…' : 'closed'}
        </span>
        <span className="term-hint">
          {kind === 'orchestrator'
            ? 'project conversation · checked edits through workflows'
            : 'login shell · platform root'}
          {used ? ` · ${used}` : ''}
        </span>
        {status === 'closed' && (
          <button className="btn-sm" onClick={() => setGeneration((g) => g + 1)}>
            reconnect
          </button>
        )}
      </div>
      <div className="term-host" ref={hostRef} />
    </div>
  );
}
