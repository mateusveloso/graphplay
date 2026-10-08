from graph_issue_triage.graph import (
    route_after_check,
    route_after_classify,
    route_after_critic,
    route_after_gate,
)
from graph_issue_triage.state import Prior
from tests.conftest import approve, reject


def test_classify_skips_code_only_when_confidently_unneeded(settings):
    assert route_after_classify({"prior": Prior(needs_code=0.05)}, settings) == "writer"
    assert route_after_classify({"prior": Prior(needs_code=0.5)}, settings) == "planner"
    assert route_after_classify({"prior": Prior()}, settings) == "planner"


def test_check_verdicts(settings):
    assert route_after_check({"verdict": "uncertain", "critic_rounds": 1}, settings) == "critic"
    assert route_after_check({"verdict": "pass", "critic_rounds": 1}, settings) == "gate"
    assert route_after_check({"verdict": "reject", "critic_rounds": 1}, settings) == "writer"
    assert route_after_check({"verdict": "reject", "critic_rounds": 2}, settings) == "gate"


def test_approved_review_goes_to_gate(settings):
    state = {"reviews": [approve()], "critic_rounds": 1}
    assert route_after_critic(state, settings) == "gate"


def test_rejected_review_loops_to_writer(settings):
    state = {"reviews": [reject("vague")], "critic_rounds": 1}
    assert route_after_critic(state, settings) == "writer"


def test_exhausted_budget_goes_to_gate_even_when_rejected(settings):
    state = {"reviews": [reject("still vague")], "critic_rounds": settings.max_critic_rounds}
    assert route_after_critic(state, settings) == "gate"


def test_gate_routes_on_approval_flag():
    assert route_after_gate({"approved": True}) == "finalize"
    assert route_after_gate({"approved": False}) == "writer"
