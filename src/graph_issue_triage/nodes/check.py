from graph_issue_triage.config import Settings
from graph_issue_triage.jev import Decider, noul
from graph_issue_triage.state import Review, TriageState

# One yes/no per rule. A rubric that weighs several things at once is a prompt, not a rubric.
RUBRIC = {
    "grounded": "Every claim in the summary is supported by the evidence listed.",
    "category_matches": "The category is consistent with what the summary describes.",
    "actionable": "A maintainer could act on the next steps today without asking anything.",
}


def _render(state: TriageState) -> str:
    draft = state["draft"]
    evidence = "\n\n".join(
        f"### {e.kind}: {e.target}\n{e.content if e.error is None else f'(error: {e.error})'}"
        for e in state.get("evidence", [])
    )
    return (
        f"# Evidence\n{evidence or '(none)'}\n\n# Triage\ncategory: {draft.category}\n"
        f"summary: {draft.summary}\nnext_steps:\n" + "\n".join(f"- {s}" for s in draft.next_steps)
    )


def make_check(decider: Decider, settings: Settings):
    """Everything cheaper than a generative critic, in cost order:
    1. code: did the draft cite evidence that was never collected?
    2. decision model: one probability per rubric rule.
    The verdict is computed here, in code, from thresholds in Settings."""

    def check(state: TriageState) -> dict:
        draft = state["draft"]
        rounds = state.get("critic_rounds", 0) + 1
        known = {e.target for e in state.get("evidence", [])}

        unknown = [t for t in draft.evidence_used if t not in known]
        if unknown:
            feedback = f"evidence_used references targets that were never collected: {unknown}"
            review = Review(source="validator", approved=False, feedback=feedback)
            return {
                "reviews": [review],
                "critic_rounds": rounds,
                "verdict": "reject",
                "rubric": None,
            }

        answers = decider.decide(_render(state), {k: noul(v) for k, v in RUBRIC.items()})
        if not answers:
            return {"critic_rounds": rounds, "verdict": "uncertain", "rubric": None}

        rubric = {rule: float((answers.get(rule) or {}).get("noul", 0.5)) for rule in RUBRIC}
        failed = [rule for rule, p in rubric.items() if p < settings.rubric_fail]
        if failed:
            feedback = "; ".join(
                f"{rule}: {RUBRIC[rule]} (p={rubric[rule]:.2f})" for rule in failed
            )
            review = Review(source="rubric", approved=False, feedback=f"rules not met: {feedback}")
            return {
                "reviews": [review],
                "critic_rounds": rounds,
                "verdict": "reject",
                "rubric": rubric,
            }

        if all(p >= settings.rubric_pass for p in rubric.values()):
            scores = ", ".join(f"{rule}={p:.2f}" for rule, p in rubric.items())
            review = Review(source="rubric", approved=True, feedback=f"all rules met: {scores}")
            return {
                "reviews": [review],
                "critic_rounds": rounds,
                "verdict": "pass",
                "rubric": rubric,
            }

        return {"critic_rounds": rounds, "verdict": "uncertain", "rubric": rubric}

    return check
