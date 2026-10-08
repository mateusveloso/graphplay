from graph_issue_triage.llm import StructuredLLM
from graph_issue_triage.state import Plan, TriageState

SYSTEM = """You plan the triage of a GitHub issue. You do not answer it.
Produce up to 3 hypotheses about what the issue is really about, and the evidence
requests needed to confirm or kill each one. Prefer reading concrete files from the
listing you are given. Use search_code only when you do not know where to look.
Request only what you would actually read."""

USER = """Repository: {owner}/{repo}
Top-level listing:
{listing}

Issue #{number}: {title}
Labels: {labels}

{body}"""


def make_planner(llm: StructuredLLM):
    def planner(state: TriageState) -> dict:
        issue = state["issue"]
        prompt = USER.format(
            owner=issue.owner,
            repo=issue.repo,
            listing="\n".join(issue.root_listing),
            number=issue.number,
            title=issue.title,
            labels=", ".join(issue.labels) or "none",
            body=issue.body,
        )
        plan: Plan = llm.ask(Plan, SYSTEM, prompt)
        return {"plan": plan}

    return planner
