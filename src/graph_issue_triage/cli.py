import argparse
import json
import sys
import uuid
from pathlib import Path

from langgraph.checkpoint.sqlite import SqliteSaver
from langgraph.types import Command

from graph_issue_triage.config import Settings
from graph_issue_triage.github import GitHubClient
from graph_issue_triage.graph import build_graph
from graph_issue_triage.jev import JevClient, NoDecider
from graph_issue_triage.llm import LangChainLLM, Models


def _print_interrupt(result: dict, thread_id: str) -> None:
    payload = result["__interrupt__"][0].value
    print(f"\n=== draft for {payload['issue_ref']} (critic rounds: {payload['critic_rounds']}) ===")
    if payload.get("prior"):
        print("prior:", json.dumps(payload["prior"], ensure_ascii=False))
    if payload.get("rubric"):
        print("rubric:", json.dumps(payload["rubric"], ensure_ascii=False))
    print(json.dumps(payload["draft"], indent=2, ensure_ascii=False))
    if payload["last_review"]:
        print("\nlast review:", json.dumps(payload["last_review"], ensure_ascii=False))
    print(f"\nthread: {thread_id}")
    print(f"approve: triage resume {thread_id} --approve")
    print(f'reject:  triage resume {thread_id} --reject "what to change"')


def _report(result: dict, thread_id: str) -> None:
    if "__interrupt__" in result:
        _print_interrupt(result, thread_id)
    else:
        print(f"written: {result['output_path']}")


def _compiled(settings: Settings, saver: SqliteSaver):
    models = Models(
        planner=LangChainLLM(settings.model_planner),
        writer=LangChainLLM(settings.model_writer),
        critic=LangChainLLM(settings.model_critic),
    )
    repo = GitHubClient(settings.github_token)
    decider = JevClient(settings.typesafe_api_key) if settings.typesafe_api_key else NoDecider()
    return build_graph(models, repo, decider, settings).compile(checkpointer=saver)


def cmd_run(args: argparse.Namespace, settings: Settings) -> None:
    thread_id = args.thread_id or uuid.uuid4().hex[:12]
    config = {"configurable": {"thread_id": thread_id}}
    settings.state_dir.mkdir(parents=True, exist_ok=True)
    with SqliteSaver.from_conn_string(str(settings.checkpoint_db)) as saver:
        graph = _compiled(settings, saver)
        result = graph.invoke({"issue_ref": args.issue}, config)
    _report(result, thread_id)


def cmd_resume(args: argparse.Namespace, settings: Settings) -> None:
    config = {"configurable": {"thread_id": args.thread_id}}
    decision = {"approve": True} if args.approve else {"approve": False, "feedback": args.reject}
    with SqliteSaver.from_conn_string(str(settings.checkpoint_db)) as saver:
        graph = _compiled(settings, saver)
        if graph.get_state(config).created_at is None:
            sys.exit(f"unknown thread: {args.thread_id}")
        result = graph.invoke(Command(resume=decision), config)
    _report(result, args.thread_id)


def cmd_diagram(args: argparse.Namespace, settings: Settings) -> None:
    # Fakes are enough: the drawing only needs the graph's shape.
    graph = build_graph(
        models=Models.single(None),  # type: ignore[arg-type]
        repo=None,  # type: ignore[arg-type]
        decider=NoDecider(),
        settings=settings,
    ).compile()
    mermaid = graph.get_graph().draw_mermaid()
    Path(args.out).write_text(mermaid)
    print(mermaid)


def main(argv: list[str] | None = None) -> None:
    parser = argparse.ArgumentParser(prog="triage", description="Graph-driven GitHub issue triage")
    sub = parser.add_subparsers(dest="command", required=True)

    run = sub.add_parser("run", help="triage an issue until the human gate")
    run.add_argument("issue", help="owner/repo#number")
    run.add_argument("--thread-id", help="reuse a thread id (default: random)")
    run.set_defaults(func=cmd_run)

    resume = sub.add_parser("resume", help="answer the human gate for a paused thread")
    resume.add_argument("thread_id")
    decision = resume.add_mutually_exclusive_group(required=True)
    decision.add_argument("--approve", action="store_true")
    decision.add_argument("--reject", metavar="FEEDBACK")
    resume.set_defaults(func=cmd_resume)

    diagram = sub.add_parser("diagram", help="write the graph as Mermaid")
    diagram.add_argument("--out", default="docs/graph.mmd")
    diagram.set_defaults(func=cmd_diagram)

    args = parser.parse_args(argv)
    args.func(args, Settings())


if __name__ == "__main__":
    main()
