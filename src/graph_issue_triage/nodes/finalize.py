from langchain_core.runnables import RunnableConfig

from graph_issue_triage.config import Settings
from graph_issue_triage.state import TriageState


def render(state: TriageState) -> str:
    issue, draft = state["issue"], state["draft"]
    lines = [
        f"# Triage of {issue.owner}/{issue.repo}#{issue.number}",
        "",
        f"**{issue.title}**  ",
        f"{issue.url}",
        "",
        f"- category: `{draft.category}`",
        f"- confidence: `{draft.confidence}`",
        "",
        "## Summary",
        draft.summary,
        "",
        "## Evidence used",
        *[f"- {t}" for t in draft.evidence_used],
        "",
        "## Next steps",
        *[f"- {s}" for s in draft.next_steps],
        "",
        "## Review trail",
        *[
            f"- [{r.source}] {'approved' if r.approved else 'rejected'}: {r.feedback}"
            for r in state.get("reviews", [])
        ],
    ]
    return "\n".join(lines) + "\n"


def make_finalize(settings: Settings):
    def finalize(state: TriageState, config: RunnableConfig) -> dict:
        thread_id = config["configurable"]["thread_id"]
        out_dir = settings.runs_dir / thread_id
        out_dir.mkdir(parents=True, exist_ok=True)
        path = out_dir / "triage.md"
        path.write_text(render(state))
        return {"output_path": str(path)}

    return finalize
