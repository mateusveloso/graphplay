from collections import defaultdict, deque

import pytest

from graph_issue_triage.config import Settings
from graph_issue_triage.state import EvidenceRequest, Plan, Review, Triage


class FakeLLM:
    """Returns scripted objects per schema, in order. Records every prompt it saw."""

    def __init__(self):
        self.scripts: dict[type, deque] = defaultdict(deque)
        self.calls: list[tuple[type, str, str]] = []

    def script(self, *objects):
        for obj in objects:
            self.scripts[type(obj)].append(obj)
        return self

    def ask(self, schema, system, user):
        self.calls.append((schema, system, user))
        if not self.scripts[schema]:
            raise AssertionError(f"no scripted answer left for {schema.__name__}")
        return self.scripts[schema].popleft()


class FakeRepo:
    def __init__(self, files: dict[str, str] | None = None):
        self.files = files or {"README.md": "# demo\n", "src/app.py": "def main():\n    pass\n"}
        self.calls: list[str] = []

    def get_issue(self, owner, repo, number):
        return {
            "title": "App crashes on start",
            "body": "Running `python -m app` raises TypeError.",
            "labels": [{"name": "bug"}],
            "html_url": f"https://github.com/{owner}/{repo}/issues/{number}",
        }

    def list_root(self, owner, repo):
        return ["README.md", "src/"]

    def read_file(self, owner, repo, path):
        self.calls.append(f"read:{path}")
        if path not in self.files:
            raise FileNotFoundError(path)
        return self.files[path]

    def search_code(self, owner, repo, query):
        self.calls.append(f"search:{query}")
        return [p for p in self.files if query in self.files[p]]


@pytest.fixture
def settings(tmp_path):
    return Settings(model="fake", max_critic_rounds=2, max_evidence_requests=3, state_dir=tmp_path)


@pytest.fixture
def plan():
    return Plan(
        hypotheses=["entrypoint signature changed"],
        requests=[EvidenceRequest(kind="read_file", target="src/app.py", reason="entrypoint")],
    )


@pytest.fixture
def good_draft():
    return Triage(
        category="bug",
        summary="main() takes no args but is called with argv.",
        evidence_used=["src/app.py"],
        next_steps=["accept argv in main()"],
        confidence="high",
    )


def approve(feedback="looks grounded"):
    return Review(approved=True, feedback=feedback)


def reject(feedback):
    return Review(approved=False, feedback=feedback)
