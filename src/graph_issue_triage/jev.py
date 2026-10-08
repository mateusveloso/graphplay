"""Jev (TypeSafe AI, System One) as a decision node.

Jev does not generate text. It answers typed questions about a state with calibrated
probabilities: `noul` (yes/no -> probability of yes), `choice` (one of N options ->
choice + confidence + probabilities), `score` (ordered scale). That makes it the extreme
case of "the model is a node": a node that can only decide.

The graph never depends on Jev being up. Any failure returns None and the caller takes
the path it would have taken without a decision model.
"""

from typing import Protocol

import httpx

ENDPOINT = "https://api.typesafe.ai/v1/systemone"
MODEL = "jev-1.13.0"  # pinned: thresholds are tuned against one version


class Decider(Protocol):
    def decide(self, state: str, questions: dict[str, dict]) -> dict[str, dict] | None: ...


def noul(instructions: str, true: str = "Yes.", false: str = "No.") -> dict:
    return {
        "type": "noul",
        "instructions": instructions,
        "criteria": {"true": true, "false": false},
    }


def choice(instructions: str, criteria: dict[str, str]) -> dict:
    return {"type": "choice", "instructions": instructions, "criteria": criteria}


class JevClient:
    def __init__(self, api_key: str, timeout: float = 15.0):
        self._http = httpx.Client(
            base_url=ENDPOINT,
            headers={"Authorization": f"Bearer {api_key}"},
            timeout=timeout,
        )

    def decide(self, state: str, questions: dict[str, dict]) -> dict[str, dict] | None:
        body = {"model": MODEL, "state": state, "questions": questions}
        try:
            response = self._http.post("", json=body)
            response.raise_for_status()
        except httpx.HTTPError:
            return None
        return response.json().get("answers")


class NoDecider:
    """Used when TYPESAFE_API_KEY is absent: every decision falls through to the default path."""

    def decide(self, state: str, questions: dict[str, dict]) -> dict[str, dict] | None:
        return None
