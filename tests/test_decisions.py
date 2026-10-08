"""The decision model's answers steer the graph through code, never through a prompt."""

from langgraph.checkpoint.memory import MemorySaver

from graph_issue_triage.graph import build_graph
from graph_issue_triage.state import Plan, Review, Triage
from tests.conftest import (
    FakeDecider,
    FakeLLM,
    FakeRepo,
    approve,
    jev_choice,
    jev_noul,
    models,
    rubric_answers,
)


def _run(llm, decider, settings):
    graph = build_graph(models(llm), FakeRepo(), decider, settings).compile(
        checkpointer=MemorySaver()
    )
    return graph.invoke({"issue_ref": "octo/demo#7"}, {"configurable": {"thread_id": "d1"}})


def test_confident_no_code_skips_planner_and_collect(settings, good_draft):
    decider = FakeDecider(
        {"category": jev_choice("question", 0.92), "needs_code": jev_noul(0.05)},
        rubric_answers(),
    )
    llm = FakeLLM().script(good_draft.model_copy(update={"evidence_used": []}))

    result = _run(llm, decider, settings)

    assert [schema for schema, _, _ in llm.calls] == [Triage]  # no Plan, no Review
    assert "(no code read)" in llm.calls[0][2]
    assert "category: question (confidence 0.92)" in llm.calls[0][2]
    assert result["__interrupt__"][0].value["prior"]["category"] == "question"


def test_rubric_pass_skips_the_generative_critic(settings, plan, good_draft):
    decider = FakeDecider({"needs_code": jev_noul(0.9)}, rubric_answers(0.9, 0.95, 0.88))
    llm = FakeLLM().script(plan, good_draft)

    result = _run(llm, decider, settings)

    assert Review not in [schema for schema, _, _ in llm.calls]
    payload = result["__interrupt__"][0].value
    assert payload["last_review"]["source"] == "rubric"
    assert payload["last_review"]["approved"] is True
    assert payload["rubric"]["actionable"] == 0.88


def test_rubric_fail_rejects_with_templated_feedback(settings, plan, good_draft):
    decider = FakeDecider(
        {"needs_code": jev_noul(0.9)}, rubric_answers(actionable=0.2), rubric_answers()
    )
    llm = FakeLLM().script(plan, good_draft, good_draft)

    _run(llm, decider, settings)

    writer_prompts = [user for schema, _, user in llm.calls if schema is Triage]
    assert len(writer_prompts) == 2
    assert "[rubric] rules not met: actionable" in writer_prompts[1]
    assert Review not in [schema for schema, _, _ in llm.calls]


def test_uncertain_rubric_pays_for_the_generative_critic(settings, plan, good_draft):
    decider = FakeDecider({"needs_code": jev_noul(0.9)}, rubric_answers(grounded=0.7))
    llm = FakeLLM().script(plan, good_draft, approve())

    result = _run(llm, decider, settings)

    critic_prompts = [user for schema, _, user in llm.calls if schema is Review]
    assert len(critic_prompts) == 1
    assert "grounded: 0.70" in critic_prompts[0]
    assert result["__interrupt__"][0].value["last_review"]["source"] == "critic"


def test_without_a_decider_the_graph_takes_the_full_path(settings, plan, good_draft):
    llm = FakeLLM().script(plan, good_draft, approve())

    result = _run(llm, FakeDecider(), settings)

    assert [schema for schema, _, _ in llm.calls] == [Plan, Triage, Review]
    assert result["__interrupt__"][0].value["prior"] == {
        "category": None,
        "confidence": None,
        "needs_code": None,
    }
