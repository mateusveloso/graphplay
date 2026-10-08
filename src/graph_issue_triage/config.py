import os
from dataclasses import dataclass, field
from pathlib import Path

from dotenv import load_dotenv

load_dotenv()


def _env(name: str, default: str) -> str:
    return os.getenv(name) or default


def _env_int(name: str, default: int) -> int:
    return int(_env(name, str(default)))


def _env_float(name: str, default: float) -> float:
    return float(_env(name, str(default)))


BIG = "anthropic:claude-opus-5-5"
SMALL = "anthropic:claude-haiku-5-5"


@dataclass(frozen=True)
class Settings:
    """Runtime limits and thresholds live here, in code. Prompts never own a loop bound."""

    # One model per role. Planning is bounded and cheap to get slightly wrong: small model.
    # Writing and judging are where quality is paid for: big model.
    model_planner: str = field(default_factory=lambda: _env("TRIAGE_MODEL_PLANNER", SMALL))
    model_writer: str = field(default_factory=lambda: _env("TRIAGE_MODEL_WRITER", BIG))
    model_critic: str = field(default_factory=lambda: _env("TRIAGE_MODEL_CRITIC", BIG))

    typesafe_api_key: str | None = field(
        default_factory=lambda: os.getenv("TYPESAFE_API_KEY") or None
    )
    github_token: str | None = field(default_factory=lambda: os.getenv("GITHUB_TOKEN") or None)

    max_critic_rounds: int = field(default_factory=lambda: _env_int("TRIAGE_MAX_CRITIC_ROUNDS", 2))
    max_evidence_requests: int = field(default_factory=lambda: _env_int("TRIAGE_MAX_EVIDENCE", 5))
    max_file_chars: int = 6000

    # Decision thresholds (probabilities). A noul sits at 0.5 when it does not know.
    skip_code_below: float = field(
        default_factory=lambda: _env_float("TRIAGE_SKIP_CODE_BELOW", 0.15)
    )
    rubric_pass: float = field(default_factory=lambda: _env_float("TRIAGE_RUBRIC_PASS", 0.85))
    rubric_fail: float = field(default_factory=lambda: _env_float("TRIAGE_RUBRIC_FAIL", 0.50))

    state_dir: Path = field(default_factory=lambda: Path(_env("TRIAGE_STATE_DIR", ".triage")))

    @property
    def checkpoint_db(self) -> Path:
        return self.state_dir / "checkpoints.sqlite"

    @property
    def runs_dir(self) -> Path:
        return self.state_dir / "runs"
