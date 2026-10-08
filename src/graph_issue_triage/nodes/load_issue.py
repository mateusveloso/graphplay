from graph_issue_triage.github import Repository, parse_issue_ref
from graph_issue_triage.state import Issue, TriageState


def make_load_issue(repo: Repository):
    """Deterministic entry: fetch the issue and the repo's top-level listing. No model involved."""

    def load_issue(state: TriageState) -> dict:
        owner, name, number = parse_issue_ref(state["issue_ref"])
        raw = repo.get_issue(owner, name, number)
        issue = Issue(
            owner=owner,
            repo=name,
            number=number,
            title=raw["title"],
            body=raw.get("body") or "",
            labels=[label["name"] for label in raw.get("labels", [])],
            url=raw["html_url"],
            root_listing=repo.list_root(owner, name),
        )
        return {"issue": issue, "evidence": [], "critic_rounds": 0, "approved": False}

    return load_issue
