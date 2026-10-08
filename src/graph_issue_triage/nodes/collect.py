from graph_issue_triage.config import Settings
from graph_issue_triage.github import Repository
from graph_issue_triage.state import Evidence, TriageState


def make_collect(repo: Repository, settings: Settings):
    """Deterministic executor of the plan. The request cap is enforced here, not in a prompt."""

    def collect(state: TriageState) -> dict:
        issue = state["issue"]
        requests = state["plan"].requests[: settings.max_evidence_requests]
        evidence: list[Evidence] = []
        for request in requests:
            try:
                if request.kind == "read_file":
                    content = repo.read_file(issue.owner, issue.repo, request.target)
                    content = content[: settings.max_file_chars]
                else:
                    hits = repo.search_code(issue.owner, issue.repo, request.target)
                    content = "\n".join(hits) if hits else "(no matches)"
                evidence.append(Evidence(kind=request.kind, target=request.target, content=content))
            except Exception as exc:  # noqa: BLE001 - a failed fetch is evidence too
                evidence.append(Evidence(kind=request.kind, target=request.target, error=str(exc)))
        return {"evidence": evidence}

    return collect
