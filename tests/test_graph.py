from langgraph.checkpoint.memory import MemorySaver
from langgraph.types import Command

from graph_issue_triage.graph import build_graph
from graph_issue_triage.state import Triage
from tests.conftest import FakeDecider, FakeLLM, FakeRepo, approve, models, reject


def _run(llm, settings, thread="t1", decider=None):
    decider = decider or FakeDecider()
    graph = build_graph(models(llm), FakeRepo(), decider, settings).compile(
        checkpointer=MemorySaver()
    )
    config = {"configurable": {"thread_id": thread}}
    return graph, config, graph.invoke({"issue_ref": "octo/demo#7"}, config)


def test_happy_path_pauses_at_gate_then_writes_file(settings, plan, good_draft):
    llm = FakeLLM().script(plan, good_draft, approve())

    graph, config, result = _run(llm, settings)

    assert "__interrupt__" in result
    payload = result["__interrupt__"][0].value
    assert payload["draft"]["category"] == "bug"
    assert payload["last_review"]["approved"] is True

    final = graph.invoke(Command(resume={"approve": True}), config)
    output = settings.runs_dir / "t1" / "triage.md"
    assert final["output_path"] == str(output)
    assert "octo/demo#7" in output.read_text()
    assert "[critic] approved" in output.read_text()


def test_critic_rejection_feeds_the_next_draft(settings, plan, good_draft):
    weak = good_draft.model_copy(update={"summary": "something is broken", "confidence": "low"})
    llm = FakeLLM().script(
        plan, weak, reject("summary does not say what breaks"), good_draft, approve()
    )

    _, _, result = _run(llm, settings)

    writer_prompts = [user for schema, _, user in llm.calls if schema is Triage]
    assert len(writer_prompts) == 2
    assert "summary does not say what breaks" in writer_prompts[1]
    assert result["__interrupt__"][0].value["critic_rounds"] == 2


def test_critic_budget_is_enforced_in_code(settings, plan, good_draft):
    llm = FakeLLM().script(plan, good_draft, reject("no"), good_draft, reject("still no"))

    _, _, result = _run(llm, settings)

    payload = result["__interrupt__"][0].value
    assert payload["critic_rounds"] == settings.max_critic_rounds
    assert payload["last_review"]["approved"] is False
    writer_calls = [schema for schema, _, _ in llm.calls if schema is Triage]
    assert len(writer_calls) == settings.max_critic_rounds


def test_validator_rejects_invented_evidence_without_calling_the_model(settings, plan, good_draft):
    invented = good_draft.model_copy(update={"evidence_used": ["src/does_not_exist.py"]})
    llm = FakeLLM().script(plan, invented, good_draft, approve())

    _, _, result = _run(llm, settings)

    sources = [r.source for r in graph_state(result)]
    assert sources == ["validator", "critic"]


def test_human_rejection_loops_back_to_writer_and_resets_budget(settings, plan, good_draft):
    llm = FakeLLM().script(plan, good_draft, approve(), good_draft, approve())

    graph, config, _ = _run(llm, settings)
    result = graph.invoke(
        Command(resume={"approve": False, "feedback": "mention the version"}), config
    )

    assert "__interrupt__" in result
    payload = result["__interrupt__"][0].value
    assert payload["critic_rounds"] == 1
    writer_prompts = [user for schema, _, user in llm.calls if schema is Triage]
    assert "[human] mention the version" in writer_prompts[1]


def graph_state(result):
    return result["reviews"]
