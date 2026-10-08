from graph_issue_triage.llm import StructuredLLM
from graph_issue_triage.state import Review, TriageState

SYSTEM = """You review a triage written by another model. Approve only if:
1. every claim in the summary is supported by the evidence shown;
2. the category matches the summary;
3. next steps are concrete enough for a maintainer to act on today.
When rejecting, say exactly what to change. Do not rewrite the triage yourself."""

USER = """## Evidence available
{evidence}

## Triage under review
category: {category}
confidence: {confidence}
summary: {summary}
evidence_used: {evidence_used}
next_steps:
{next_steps}"""


def make_critic(llm: StructuredLLM):
    def critic(state: TriageState) -> dict:
        draft = state["draft"]
        rounds = state.get("critic_rounds", 0) + 1
        known_targets = {e.target for e in state.get("evidence", [])}

        # Code checks what code can check. The model only sees drafts that pass.
        unknown = [t for t in draft.evidence_used if t not in known_targets]
        if unknown:
            feedback = f"evidence_used references targets that were never collected: {unknown}"
            review = Review(source="validator", approved=False, feedback=feedback)
            return {"reviews": [review], "critic_rounds": rounds}

        prompt = USER.format(
            evidence="\n".join(
                f"- {e.kind}: {e.target}" + (" (failed)" if e.error else "")
                for e in state.get("evidence", [])
            ),
            category=draft.category,
            confidence=draft.confidence,
            summary=draft.summary,
            evidence_used=", ".join(draft.evidence_used) or "none",
            next_steps="\n".join(f"- {s}" for s in draft.next_steps),
        )
        review: Review = llm.ask(Review, SYSTEM, prompt)
        review.source = "critic"
        return {"reviews": [review], "critic_rounds": rounds}

    return critic
