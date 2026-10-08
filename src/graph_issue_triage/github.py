import base64
from typing import Protocol

import httpx

API = "https://api.github.com"


class Repository(Protocol):
    """What the graph needs from GitHub. Tests implement this with a fake."""

    def get_issue(self, owner: str, repo: str, number: int) -> dict: ...
    def list_root(self, owner: str, repo: str) -> list[str]: ...
    def read_file(self, owner: str, repo: str, path: str) -> str: ...
    def search_code(self, owner: str, repo: str, query: str) -> list[str]: ...


class GitHubClient:
    def __init__(self, token: str | None = None, timeout: float = 20.0):
        headers = {"Accept": "application/vnd.github+json", "User-Agent": "graph-issue-triage"}
        if token:
            headers["Authorization"] = f"Bearer {token}"
        self._http = httpx.Client(base_url=API, headers=headers, timeout=timeout)

    def _get(self, path: str, **params) -> dict | list:
        response = self._http.get(path, params=params)
        response.raise_for_status()
        return response.json()

    def get_issue(self, owner: str, repo: str, number: int) -> dict:
        return self._get(f"/repos/{owner}/{repo}/issues/{number}")

    def list_root(self, owner: str, repo: str) -> list[str]:
        entries = self._get(f"/repos/{owner}/{repo}/contents/")
        return [f"{e['path']}/" if e["type"] == "dir" else e["path"] for e in entries]

    def read_file(self, owner: str, repo: str, path: str) -> str:
        data = self._get(f"/repos/{owner}/{repo}/contents/{path}")
        if isinstance(data, list):
            return "\n".join(e["path"] for e in data)
        if data.get("encoding") != "base64":
            raise ValueError(f"{path}: unsupported encoding {data.get('encoding')!r}")
        return base64.b64decode(data["content"]).decode("utf-8", errors="replace")

    def search_code(self, owner: str, repo: str, query: str) -> list[str]:
        data = self._get("/search/code", q=f"{query} repo:{owner}/{repo}", per_page=10)
        return [item["path"] for item in data.get("items", [])]


def parse_issue_ref(ref: str) -> tuple[str, str, int]:
    """'owner/repo#123' -> ('owner', 'repo', 123)."""
    try:
        full, number = ref.split("#")
        owner, repo = full.split("/")
        return owner, repo, int(number)
    except ValueError as exc:
        raise ValueError(f"issue ref must look like owner/repo#123, got {ref!r}") from exc
