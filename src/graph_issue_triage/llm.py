from dataclasses import dataclass
from typing import Any, Protocol

from langchain.chat_models import init_chat_model
from langchain_core.messages import HumanMessage, SystemMessage


class StructuredLLM(Protocol):
    """The only thing a node may ask of a generative model: a typed object for a prompt."""

    def ask(self, schema: type, system: str, user: str) -> Any: ...


class LangChainLLM:
    def __init__(self, model: str):
        self._llm = init_chat_model(model)

    def ask(self, schema: type, system: str, user: str) -> Any:
        structured = self._llm.with_structured_output(schema)
        return structured.invoke([SystemMessage(content=system), HumanMessage(content=user)])


@dataclass(frozen=True)
class Models:
    """One generative model per role. Roles, not nodes: the graph decides who calls what."""

    planner: StructuredLLM
    writer: StructuredLLM
    critic: StructuredLLM

    @classmethod
    def single(cls, llm: StructuredLLM) -> "Models":
        return cls(planner=llm, writer=llm, critic=llm)
