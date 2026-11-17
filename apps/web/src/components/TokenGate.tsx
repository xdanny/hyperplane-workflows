import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { api } from '../api';

type Phase = 'checking' | 'need-token' | 'ok' | 'error';

/**
 * TokenGate blocks the app until the server confirms we're authenticated. When a
 * token is required (remote access) it shows a single-field login that exchanges
 * the token for an hp_token cookie. Local/loopback use needs no token and falls
 * straight through.
 */
export function TokenGate({ children }: { children: ReactNode }) {
  const [phase, setPhase] = useState<Phase>('checking');
  const [token, setToken] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');

  async function check() {
    try {
      const s = await api.authCheck();
      setPhase(!s.authRequired || s.authed ? 'ok' : 'need-token');
    } catch {
      setPhase('error');
    }
  }

  useEffect(() => {
    check();
  }, []);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!token.trim()) return;
    setSubmitting(true);
    setError('');
    try {
      await api.login(token.trim());
      setToken('');
      await check();
    } catch {
      setError('Invalid token.');
    } finally {
      setSubmitting(false);
    }
  }

  if (phase === 'ok') return <>{children}</>;

  if (phase === 'checking') {
    return (
      <div className="gate">
        <div className="gate-card">
          <div className="gate-mark">⌗</div>
          <p className="gate-msg">Connecting…</p>
        </div>
      </div>
    );
  }

  if (phase === 'error') {
    return (
      <div className="gate">
        <div className="gate-card">
          <div className="gate-mark">⌗</div>
          <p className="gate-msg">Can’t reach the Hyperplane API.</p>
          <button className="btn" onClick={() => { setPhase('checking'); check(); }}>
            Retry
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="gate">
      <form className="gate-card" onSubmit={submit}>
        <div className="gate-mark">⌗</div>
        <div className="gate-title">Hyperplane</div>
        <p className="gate-msg">Enter your access token to continue.</p>
        <input
          className="gate-input"
          type="password"
          autoFocus
          placeholder="access token"
          value={token}
          onChange={(e) => setToken(e.target.value)}
        />
        {error && <div className="gate-error">{error}</div>}
        <button className="btn" type="submit" disabled={submitting || !token.trim()}>
          {submitting ? 'Unlocking…' : 'Unlock'}
        </button>
      </form>
    </div>
  );
}
