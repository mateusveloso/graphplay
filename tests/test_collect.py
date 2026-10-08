from graph_issue_triage.nodes.collect import make_collect
from graph_issue_triage.state import EvidenceRequest, Issue, Plan
from tests.conftest import FakeRepo


def _issue():
    return Issue(
        owner="o", repo="r", number=1, title="t", body="", labels=[], url="u", root_listing=[]
    )


def test_collect_caps_requests_in_code(settings):
    repo = FakeRepo()
    requests = [EvidenceRequest(kind="read_file", target="README.md", reason="x")] * 10
    state = {"issue": _issue(), "plan": Plan(hypotheses=[], requests=requests)}

    evidence = make_collect(repo, settings)(state)["evidence"]

    assert len(evidence) == settings.max_evidence_requests
    assert len(repo.calls) == settings.max_evidence_requests


def test_failed_fetch_becomes_evidence_not_exception(settings):
    repo = FakeRepo()
    plan = Plan(
        hypotheses=[], requests=[EvidenceRequest(kind="read_file", target="missing.py", reason="x")]
    )

    [item] = make_collect(repo, settings)({"issue": _issue(), "plan": plan})["evidence"]

    assert item.content is None
    assert "missing.py" in item.error


def test_search_results_are_joined_paths(settings):
    repo = FakeRepo()
    plan = Plan(
        hypotheses=[], requests=[EvidenceRequest(kind="search_code", target="def main", reason="x")]
    )

    [item] = make_collect(repo, settings)({"issue": _issue(), "plan": plan})["evidence"]

    assert item.content == "src/app.py"
