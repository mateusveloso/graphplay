import os
from dataclasses import dataclass, field
from pathlib import Path

from dotenv import load_dotenv

load_dotenv()


def _env_int(name: str, default: int) -> int:
    return int(os.getenv(name, str(default)))


@dataclass(frozen=True)
class Settings:
    """Runtime limits live here, in code. Prompts never own a loop bound."""

    model: str = field(
        default_factory=lambda: os.getenv("TRIAGE_MODEL", "anthropic:claude-opus-5-5")
    )
    github_token: str | None = field(default_factory=lambda: os.getenv("GITHUB_TOKEN") or None)
    max_critic_rounds: int = field(default_factory=lambda: _env_int("TRIAGE_MAX_CRITIC_ROUNDS", 2))
    max_evidence_requests: int = field(default_factory=lambda: _env_int("TRIAGE_MAX_EVIDENCE", 5))
    max_file_chars: int = 6000
    state_dir: Path = field(default_factory=lambda: Path(os.getenv("TRIAGE_STATE_DIR", ".triage")))

    @property
    def checkpoint_db(self) -> Path:
        return self.state_dir / "checkpoints.sqlite"

    @property
    def runs_dir(self) -> Path:
        return self.state_dir / "runs"
