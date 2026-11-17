import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { api, EvalsetGenerateError } from '../api';

/**
 * "Generate practice test" — the remedy for a blocked Approve, on the card
 * where Approve is blocked (D-04).
 *
 * The gate itself is right: a learned draft enters every prompt that resolves
 * the skill, and one backed only by a run that passed has never been tested on
 * anything. What was wrong was the remedy — a terminal command printed in a web
 * page, which meant thirteen of thirteen skills had no evalset and the
 * improvement loop's promise to score refinements was theatre.
 *
 * This runs the SAME generator `hyperplane eval gen <skillId>` runs, so the
 * button and the command produce the same artifact. Three things it is careful
 * about:
 *   · it says the call is slow and disables itself while pending, because a
 *     second click would generate a second evalset;
 *   · a 409 is not an error to retry — it means one already exists, and the
 *     answer offered is an explicit REPLACE, never an automatic one;
 *   · generating a practice test does not approve anything. It unlocks the
 *     gate; the person still decides.
 */
export function GeneratePracticeTest({ skillId, onGenerated }: {
  skillId: string;
  /** Called after a successful generation, so the card can refresh its gate. */
  onGenerated?: () => void;
}) {
  const queryClient = useQueryClient();
  const [replacing, setReplacing] = useState(false);

  const generate = useMutation({
    mutationFn: (replace: boolean) => api.generateEvalset(skillId, { replace }),
    onSuccess: () => {
      setReplacing(false);
      // The gate reads the evalset list; both surfaces that hold an Approve
      // button share that one query key.
      void queryClient.invalidateQueries({ queryKey: ['evalsets'] });
      void queryClient.invalidateQueries({ queryKey: ['skills'] });
      onGenerated?.();
    },
    onError: (error) => {
      if (error instanceof EvalsetGenerateError && error.alreadyExists) setReplacing(true);
    },
  });

  const exists = generate.error instanceof EvalsetGenerateError && generate.error.alreadyExists;

  return (
    <div className="skill-evalset">
      <div className="acts">
        <button
          className="btn btn--sm"
          disabled={generate.isPending}
          onClick={() => generate.mutate(false)}
          aria-label={`Generate a practice test for ${skillId}`}
        >
          {generate.isPending ? 'Generating practice test…' : 'Generate practice test'}
        </button>
        {exists && replacing && (
          <button
            className="btn btn--sm btn--ghost"
            disabled={generate.isPending}
            onClick={() => generate.mutate(true)}
            aria-label={`Replace the existing practice test for ${skillId}`}
          >
            Replace the existing one
          </button>
        )}
      </div>

      {generate.isPending && (
        <p role="status" className="faint">
          Writing example inputs and the results a good answer must give. This runs a model
          conversation and can take several minutes; nothing is approved by it.
        </p>
      )}

      {generate.isSuccess && (
        <p role="status">
          Practice test created — {generate.data.cases.length} case{generate.data.cases.length === 1 ? '' : 's'}.
          Read it in <Link to="/knowledge">Knowledge</Link>, under Skill Lab, before approving: a
          test nobody has read is not evidence that the method works.
        </p>
      )}

      {generate.error && (
        <p role="alert">
          {exists
            ? 'This skill already has a practice test. Read the existing one before replacing it — replacing discards the cases it holds.'
            : `The practice test could not be generated. ${generate.error.message}`}
        </p>
      )}
    </div>
  );
}
