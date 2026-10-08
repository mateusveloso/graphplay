from langgraph.types import interrupt

from graph_issue_triage.state import Review, TriageState


def gate(state: TriageState) -> dict:
    """Human checkpoint. The graph stops here and is resumed by the CLI with a decision."""
    draft = state["draft"]
    decision = interrupt(
        {
            "issue_ref": state["issue_ref"],
            "draft": draft.model_dump(),
            "critic_rounds": state.get("critic_rounds", 0),
            "last_review": state["reviews"][-1].model_dump() if state.get("reviews") else None,
        }
    )
    if decision.get("approve"):
        return {"approved": True}
    feedback = decision.get("feedback") or "rejected without feedback"
    review = Review(source="human", approved=False, feedback=feedback)
    # A human rejection restarts the critic budget for the new draft.
    return {"approved": False, "reviews": [review], "critic_rounds": 0}
