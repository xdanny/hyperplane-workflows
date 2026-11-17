/**
 * useApprovalGate — the evalset rule, asked where the decision is actually made.
 *
 * A learned draft cannot enter prompts until an evalset names it: approval puts
 * an instruction into every prompt that resolves the skill, and a proposal
 * backed only by a run that passed has never been tested on anything. The server
 * enforces that (409 from POST /api/skills/{id}/approve — see
 * internal/knowledge/extract.go, ErrNoEvalset).
 *
 * The rule used to be STATED only in Skill Lab's detail view, while the two
 * surfaces that carry the Approve button — the Inbox's Learned queue and the
 * Knowledge registry — rendered it enabled with no hint at all. A button that
 * looks available and then refuses teaches the rule by failure; the surfaces
 * that hold the decision now hold the gate too.
 *
 * The source is the evalset list, which is the same lookup the server does and
 * the same one Skill Lab already makes — one shared query key, so asking on two
 * more surfaces costs one request, not three.
 *
 * Two deliberate abstentions:
 *   · while the list is loading or failed, NOTHING is blocked. "Unknown" must
 *     not read as "no evalset", which would refuse a legitimate approval on a
 *     network hiccup; the 409 remains the backstop that cannot be raced.
 *   · a HAND-WRITTEN draft (no supporting runs) is exempt, exactly as the server
 *     is — the judgement this rule stands in for was supplied by whoever wrote
 *     the instruction.
 */
import { useQuery } from '@tanstack/react-query';
import { api, isDraftSkill, skillLineage } from '../api';
import type { Evalset, Skill } from '../api';

export interface ApprovalGate {
  /** True when Approve must be refused on this surface. */
  blocked: boolean;
  /** The sentence to render inline, next to the disabled button. */
  reason?: string;
  /** The command that clears the block — shown as the code it is. */
  command?: string;
}

const OPEN: ApprovalGate = { blocked: false };

/** The block as one sentence, for a tooltip or an aria-label. Not every block
 *  has a command that clears it — a draft learned from a rehearsal has nothing
 *  anyone can type — so the colon and the command only appear when there is one.
 *  The reason ends by saying where the command is typed, so the colon reads as
 *  the introduction it is rather than as an em dash between two fragments. */
export function gateSentence(gate: ApprovalGate): string {
  if (!gate.blocked) return '';
  return gate.command ? `${gate.reason}: ${gate.command}` : (gate.reason ?? '');
}

/** Returns a predicate answering "may this draft be approved from here?". */
export function useApprovalGate(): (skill: Skill) => ApprovalGate {
  const { data, isSuccess } = useQuery<Evalset[]>({
    queryKey: ['evalsets'],
    queryFn: () => api.evals(),
    staleTime: 30_000,
  });

  const withEvalset = new Set(
    (data ?? []).map((es) => es.skill_ref).filter((ref): ref is string => Boolean(ref)),
  );

  return (skill: Skill): ApprovalGate => {
    if (!isDraftSkill(skill)) return OPEN;
    const lineage = skillLineage(skill);
    // A draft extracted from a REHEARSAL is refused first and unconditionally —
    // including while the evalset list is still loading, because unlike a
    // missing practice test this is not a gap somebody can close. Nothing was
    // executed, so there is no method here that worked. The server refuses it
    // too (409, knowledge.ErrRehearsalSource).
    if (lineage.sourceRunRehearsal) {
      return {
        blocked: true,
        reason:
          'Blocked: this was learned from a REHEARSAL — its outputs were stand-ins, so the draft has no real evidence. Run the delivery for real and let it be learned from again',
      };
    }
    if (!isSuccess) return OPEN;
    // Learned ⇔ the platform can name the runs it came from. A hand-written
    // draft has none and is exempt.
    if (lineage.supportRuns === 0) return OPEN;
    if (withEvalset.has(skill.id)) return OPEN;
    return {
      blocked: true,
      // A blocker that hands the reader a command has to say what the missing
      // thing IS and where the command goes — "hyperplane eval gen …" in a web
      // page is not self-evidently a terminal command (Q-30).
      reason:
        'Approval is blocked until this skill has a practice test (an evalset: example inputs with the results a good answer must give). Generate one in a terminal',
      command: `hyperplane eval gen ${skill.id}`,
    };
  };
}
