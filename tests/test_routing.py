from graph_issue_triage.graph import route_after_critic, route_after_gate
from tests.conftest import approve, reject


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
