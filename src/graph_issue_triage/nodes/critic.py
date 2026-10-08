from graph_issue_triage.llm import StructuredLLM
from graph_issue_triage.state import Review, TriageState

SYSTEM = """You review a triage written by another model. Approve only if:
1. every claim in the summary is supported by the evidence shown;
2. the category matches the summary;
3. next steps are concrete enough for a maintainer to act on today.
When rejecting, say exactly what to change. Do not rewrite the triage yourself."""

USER = """## Evidence available
{evidence}

## Rubric probabilities from the decision model (0.5 means it could not tell)
{rubric}

## Triage under review
category: {category}
confidence: {confidence}
summary: {summary}
evidence_used: {evidence_used}
next_steps:
{next_steps}"""


def make_critic(llm: StructuredLLM):
    """Generative judgment, paid only for drafts the cheaper checks could not settle."""

    def critic(state: TriageState) -> dict:
        draft = state["draft"]
        rubric = state.get("rubric") or {}
        prompt = USER.format(
            evidence="\n".join(
                f"- {e.kind}: {e.target}" + (" (failed)" if e.error else "")
                for e in state.get("evidence", [])
            ),
            rubric="\n".join(f"- {k}: {v:.2f}" for k, v in rubric.items()) or "(not available)",
            category=draft.category,
            confidence=draft.confidence,
            summary=draft.summary,
            evidence_used=", ".join(draft.evidence_used) or "none",
            next_steps="\n".join(f"- {s}" for s in draft.next_steps),
        )
        review: Review = llm.ask(Review, SYSTEM, prompt)
        review.source = "critic"
        return {"reviews": [review]}

    return critic
