import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../api';
import type { PersonaReview, PersonaView } from '../api';
import { PersonaVerdict } from './PersonaVerdict';

/**
 * "Ask a reviewer" — requesting an audit persona's read of a finished run,
 * from the page where the run is being read (D-10).
 *
 * The personas existed, the endpoint existed, and every charter's page said
 * "0 reviews recorded" because no surface in the app could ask for one. The
 * remedy was a curl command, which is the same defect as D-04's terminal-only
 * evalset: a capability nobody can reach is a capability the product does not
 * have.
 *
 * What this is NOT: a gate. A persona verdict is advisory (spec AI-12) — it
 * never blocks a sign-off, never changes a recorded check result, and never
 * substitutes for the person deciding. It is one more competent reading placed
 * beside the measured evidence, and it is labelled as a model's judgment
 * wherever it appears.
 */
export function AskReviewer({ runId }: { runId: string }) {
  const queryClient = useQueryClient();
  const target = `run:${runId}`;
  const [persona, setPersona] = useState('');

  const personas = useQuery<PersonaView[]>({
    queryKey: ['personas'],
    queryFn: () => api.personas(),
    staleTime: 60_000,
  });
  const reviews = useQuery<PersonaReview[]>({
    queryKey: ['personaReviews', target],
    // ?run=<id> — the same question `target: "run:<id>"` asks, spelled the way
    // the server offers it to a run page.
    queryFn: () => api.runPersonaReviews(runId),
  });

  const request = useMutation({
    mutationFn: (id: string) => api.personaReview(id, target),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['personaReviews', target] });
      void queryClient.invalidateQueries({ queryKey: ['personas'] });
    },
  });

  const available = Array.isArray(personas.data) ? personas.data : [];
  const recorded = Array.isArray(reviews.data) ? reviews.data : [];
  const chosen = available.find((view) => view.id === persona) ?? available[0];

  return (
    <section className="panel sdk-run-section run-reviewer" aria-label="Ask a reviewer">
      <h2>Ask a reviewer</h2>
      <p>
        A registered reviewer reads what this run delivered and records a verdict against its own
        stated bar. The verdict is advisory: it changes no recorded check result, gates nothing, and
        is a model’s judgment rather than an independent verification.
      </p>

      {personas.isPending && <p role="status">Loading reviewers…</p>}
      {personas.error && (
        <p role="alert">
          Could not load the reviewers. {personas.error.message}{' '}
          <button className="btn btn--sm" onClick={() => void personas.refetch()}>Retry</button>
        </p>
      )}
      {personas.isSuccess && available.length === 0 && (
        <p className="faint">No reviewers are registered on this installation.</p>
      )}

      {available.length > 0 && (
        <>
          <label htmlFor="run-reviewer">Reviewer</label>
          <select
            id="run-reviewer"
            className="input"
            value={chosen?.id ?? ''}
            disabled={request.isPending}
            aria-describedby="run-reviewer-competence"
            onChange={(event) => setPersona(event.target.value)}
          >
            {available.map((view) => (
              <option key={view.id} value={view.id}>
                {view.name}{view.bar ? ` — ${view.bar}` : ''}
              </option>
            ))}
          </select>
          {/* What the charter claims competence over, in its own sentences.
              The short `domains` labels read as a run-on because the claims
              contain commas of their own — see PersonaCompetence.bullets. */}
          <div id="run-reviewer-competence" className="faint run-reviewer-competence">
            {(chosen?.competence?.bullets?.length ?? 0) > 0 ? (
              <ul>{chosen!.competence.bullets!.map((line, index) => <li key={index}>{line}</li>)}</ul>
            ) : (
              <p>{chosen?.competence?.domains?.join(', ') || 'This charter states no domain claim.'}</p>
            )}
            <p>{chosen?.reviews ?? 0} verdict{(chosen?.reviews ?? 0) === 1 ? '' : 's'} recorded by this reviewer so far.</p>
          </div>
          <div className="sdk-run-actions">
            <button
              className="btn"
              disabled={request.isPending || !chosen}
              onClick={() => chosen && request.mutate(chosen.id)}
            >
              {request.isPending ? 'Reviewing…' : 'Review this run'}
            </button>
          </div>
          {/* The call is a model conversation and takes minutes. Saying so is
              what stops a second click, which would record a second verdict. */}
          {request.isPending && (
            <p role="status">
              {chosen?.name} is reading this run. This runs a model conversation and can take a few
              minutes; nothing else about the run changes while it does.
            </p>
          )}
          {request.error && <p role="alert">The review could not be recorded. {request.error.message}</p>}
          {request.isSuccess && <p role="status">Verdict recorded below.</p>}
        </>
      )}

      <h3>Reviews recorded</h3>
      {reviews.error ? (
        <p role="alert">
          Could not load recorded reviews. {reviews.error.message}{' '}
          <button className="btn btn--sm" onClick={() => void reviews.refetch()}>Retry</button>
        </p>
      ) : recorded.length === 0 ? (
        <p className="faint">No reviewer has recorded a verdict on this run.</p>
      ) : (
        <PersonaVerdict reviews={recorded} />
      )}
    </section>
  );
}
