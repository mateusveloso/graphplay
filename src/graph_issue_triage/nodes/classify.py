from graph_issue_triage.jev import Decider, choice, noul
from graph_issue_triage.state import Prior, TriageState

CATEGORIES = {
    "bug": "Something that used to work or is documented to work does not.",
    "feature": "A request for behaviour the project does not have.",
    "question": "The author asks how to do something; nothing is broken.",
    "docs": "The documentation is wrong, missing or unclear.",
    "needs_info": "Not enough detail to tell what the author wants or what happens.",
}

QUESTIONS = {
    "category": choice("Which kind of issue is this?", CATEGORIES),
    "needs_code": noul(
        "Does answering this issue require reading the project's source code?",
        true="A maintainer would have to open source files to answer.",
        false="The issue can be answered from its text and the documentation alone.",
    ),
}


def make_classify(decider: Decider):
    """Decision model, not generative: a category with confidence and one yes/no.
    Without a decider the prior is empty and the graph takes the full path."""

    def classify(state: TriageState) -> dict:
        issue = state["issue"]
        text = f"# {issue.title}\nlabels: {', '.join(issue.labels) or 'none'}\n\n{issue.body}"
        answers = decider.decide(text, QUESTIONS)
        if not answers:
            return {"prior": Prior()}
        category = answers.get("category") or {}
        needs_code = answers.get("needs_code") or {}
        return {
            "prior": Prior(
                category=category.get("choice"),
                confidence=category.get("confidence"),
                needs_code=needs_code.get("noul"),
            )
        }

    return classify
