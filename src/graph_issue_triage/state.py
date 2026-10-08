import operator
from typing import Annotated, Literal, TypedDict

from pydantic import BaseModel, Field


class Issue(BaseModel):
    owner: str
    repo: str
    number: int
    title: str
    body: str
    labels: list[str]
    url: str
    root_listing: list[str] = Field(description="Top-level paths of the repository.")


class EvidenceRequest(BaseModel):
    kind: Literal["read_file", "search_code"]
    target: str = Field(description="A file path for read_file, a search query for search_code.")
    reason: str


class Plan(BaseModel):
    hypotheses: list[str] = Field(max_length=3)
    requests: list[EvidenceRequest]


class Evidence(BaseModel):
    kind: str
    target: str
    content: str | None = None
    error: str | None = None


class Triage(BaseModel):
    category: Literal["bug", "feature", "question", "docs", "needs_info"]
    summary: str
    evidence_used: list[str] = Field(
        description="Targets from the evidence list that support this."
    )
    next_steps: list[str]
    confidence: Literal["low", "medium", "high"]


class Review(BaseModel):
    source: Literal["validator", "critic", "human"] = "critic"
    approved: bool
    feedback: str


class TriageState(TypedDict, total=False):
    issue_ref: str
    issue: Issue
    plan: Plan
    evidence: list[Evidence]
    draft: Triage
    reviews: Annotated[list[Review], operator.add]
    critic_rounds: int
    approved: bool
    output_path: str
