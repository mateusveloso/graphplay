from graph_issue_triage.llm import StructuredLLM
from graph_issue_triage.state import Evidence, Review, Triage, TriageState

SYSTEM = """You write the triage of a GitHub issue for a maintainer.
Ground every claim in the evidence provided. If the evidence does not support a
conclusion, say so and classify as needs_info. In evidence_used, list only targets
that appear in the evidence section, verbatim. Be short and specific."""

USER = """Issue #{number}: {title}
Labels: {labels}

{body}
{prior}
## Hypotheses
{hypotheses}

## Evidence
{evidence}
{feedback}"""


def _render_evidence(items: list[Evidence]) -> str:
    chunks = []
    for item in items:
        header = f"### {item.kind}: {item.target}"
        body = item.content if item.error is None else f"(error: {item.error})"
        chunks.append(f"{header}\n{body}")
    return "\n\n".join(chunks) or "(none)"


def _render_feedback(reviews: list[Review]) -> str:
    pending = [r for r in reviews if not r.approved]
    if not pending:
        return ""
    lines = [f"- [{r.source}] {r.feedback}" for r in pending]
    return "\n## Feedback on previous drafts (address all of it)\n" + "\n".join(lines)


def _render_prior(state: TriageState) -> str:
    prior = state.get("prior")
    if not prior or prior.category is None:
        return ""
    return (
        f"\n## Prior from the decision model (before any evidence)\n"
        f"category: {prior.category} (confidence {prior.confidence:.2f})\n"
    )


def make_writer(llm: StructuredLLM):
    def writer(state: TriageState) -> dict:
        issue = state["issue"]
        plan = state.get("plan")
        prompt = USER.format(
            number=issue.number,
            title=issue.title,
            labels=", ".join(issue.labels) or "none",
            body=issue.body,
            prior=_render_prior(state),
            hypotheses="\n".join(f"- {h}" for h in plan.hypotheses) if plan else "(no code read)",
            evidence=_render_evidence(state.get("evidence", [])),
            feedback=_render_feedback(state.get("reviews", [])),
        )
        draft: Triage = llm.ask(Triage, SYSTEM, prompt)
        return {"draft": draft}

    return writer
