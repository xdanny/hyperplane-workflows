import { SIGN_OFF_CONSEQUENCE, SIGN_OFF_LABEL } from '../signOff';
import type { SignOffPolicy } from '../signOff';
import { SIGN_OFF_POLICIES } from '../api';

/**
 * "When this workflow finishes" — the sign-off policy, chosen where the
 * workflow is authored (M-11).
 *
 * Every finished run asked for a signature, so twelve green runs meant twelve
 * open questions and the queue stopped meaning anything. The owner of a
 * workflow knows which of its runs need a human read; this is where they say
 * so, once, instead of dismissing the same card every week.
 *
 * Each option carries its consequence on the line below it, because a policy
 * whose effect is only discoverable by watching the Inbox afterwards is not a
 * choice anybody can make. Choosing one changes nothing until Save: like every
 * other field here, it is a setting, not an action, and it calls no model.
 */
export function WorkflowSignOff({ value, disabled, onChange }: {
  value: SignOffPolicy;
  disabled: boolean;
  onChange: (value: SignOffPolicy) => void;
}) {
  return (
    <section className="workflow-signoff" aria-labelledby="workflow-signoff-title">
      <h2 id="workflow-signoff-title">When a run of this workflow finishes</h2>
      <label htmlFor="workflow-signoff">Sign-off</label>
      <select
        id="workflow-signoff"
        className="input"
        value={value}
        disabled={disabled}
        aria-describedby="workflow-signoff-consequence"
        onChange={(event) => onChange(event.target.value as SignOffPolicy)}
      >
        {SIGN_OFF_POLICIES.map((policy) => (
          <option key={policy} value={policy}>{SIGN_OFF_LABEL[policy]}</option>
        ))}
      </select>
      <p id="workflow-signoff-consequence" className="sdk-field-hint">
        {SIGN_OFF_CONSEQUENCE[value]}
      </p>
      {value === 'auto_when_checks_pass' && (
        // Said here rather than discovered later: an automatic acceptance is a
        // statement about declared checks, and a check is a program result or a
        // model's judgment — neither is a person having read the work.
        <p className="sdk-field-hint">
          An automatic acceptance is labelled as automatic wherever it appears, and never counts as
          a person’s decision — a skill can still only be drafted from a result a person accepted.
        </p>
      )}
      <p className="sdk-field-hint">
        This applies to runs started from now on. Runs already finished keep the policy they were
        started under.
      </p>
    </section>
  );
}
