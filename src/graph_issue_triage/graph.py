from langgraph.graph import END, START, StateGraph

from graph_issue_triage.config import Settings
from graph_issue_triage.github import Repository
from graph_issue_triage.jev import Decider
from graph_issue_triage.llm import Models
from graph_issue_triage.nodes.check import make_check
from graph_issue_triage.nodes.classify import make_classify
from graph_issue_triage.nodes.collect import make_collect
from graph_issue_triage.nodes.critic import make_critic
from graph_issue_triage.nodes.finalize import make_finalize
from graph_issue_triage.nodes.gate import gate
from graph_issue_triage.nodes.load_issue import make_load_issue
from graph_issue_triage.nodes.planner import make_planner
from graph_issue_triage.nodes.writer import make_writer
from graph_issue_triage.state import TriageState


def route_after_classify(state: TriageState, settings: Settings) -> str:
    """Skip reading code only when the decision model is confident it is not needed."""
    needs_code = state["prior"].needs_code
    if needs_code is not None and needs_code <= settings.skip_code_below:
        return "writer"
    return "planner"


def route_after_check(state: TriageState, settings: Settings) -> str:
    """reject -> writer while budget lasts, else gate; pass -> gate; uncertain -> critic."""
    verdict = state["verdict"]
    if verdict == "uncertain":
        return "critic"
    if verdict == "pass" or state["critic_rounds"] >= settings.max_critic_rounds:
        return "gate"
    return "writer"


def route_after_critic(state: TriageState, settings: Settings) -> str:
    last = state["reviews"][-1]
    if last.approved or state["critic_rounds"] >= settings.max_critic_rounds:
        return "gate"
    return "writer"


def route_after_gate(state: TriageState) -> str:
    return "finalize" if state["approved"] else "writer"


def build_graph(
    models: Models, repo: Repository, decider: Decider, settings: Settings
) -> StateGraph:
    graph = StateGraph(TriageState)
    graph.add_node("load_issue", make_load_issue(repo))
    graph.add_node("classify", make_classify(decider))
    graph.add_node("planner", make_planner(models.planner))
    graph.add_node("collect", make_collect(repo, settings))
    graph.add_node("writer", make_writer(models.writer))
    graph.add_node("check", make_check(decider, settings))
    graph.add_node("critic", make_critic(models.critic))
    graph.add_node("gate", gate)
    graph.add_node("finalize", make_finalize(settings))

    graph.add_edge(START, "load_issue")
    graph.add_edge("load_issue", "classify")
    graph.add_conditional_edges(
        "classify", lambda s: route_after_classify(s, settings), ["planner", "writer"]
    )
    graph.add_edge("planner", "collect")
    graph.add_edge("collect", "writer")
    graph.add_edge("writer", "check")
    graph.add_conditional_edges(
        "check", lambda s: route_after_check(s, settings), ["writer", "critic", "gate"]
    )
    graph.add_conditional_edges(
        "critic", lambda s: route_after_critic(s, settings), ["gate", "writer"]
    )
    graph.add_conditional_edges("gate", route_after_gate, ["finalize", "writer"])
    graph.add_edge("finalize", END)
    return graph
