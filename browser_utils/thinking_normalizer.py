from dataclasses import dataclass
from typing import Any, Optional, Union
from config import DEFAULT_THINKING_BUDGET, ENABLE_THINKING_BUDGET


@dataclass
class ReasoningConfig:

    enable_reasoning: bool
    use_budget_limit: bool
    budget_tokens: Optional[int]
    raw_input: Any


def parse_reasoning_param(
    reasoning_effort: Optional[Union[int, str]],
) -> ReasoningConfig:

    if reasoning_effort is None:
        return ReasoningConfig(
            enable_reasoning=ENABLE_THINKING_BUDGET,
            use_budget_limit=ENABLE_THINKING_BUDGET,
            budget_tokens=DEFAULT_THINKING_BUDGET if ENABLE_THINKING_BUDGET else None,
            raw_input=None,
        )

    if reasoning_effort == 0 or (
        isinstance(reasoning_effort, str) and reasoning_effort.strip() == "0"
    ):
        return ReasoningConfig(
            enable_reasoning=False,
            use_budget_limit=False,
            budget_tokens=None,
            raw_input=reasoning_effort,
        )

    if isinstance(reasoning_effort, str):
        val_str = reasoning_effort.strip().lower()
        if val_str in ["none", "-1"]:
            return ReasoningConfig(
                enable_reasoning=True,
                use_budget_limit=False,
                budget_tokens=None,
                raw_input=reasoning_effort,
            )
        if val_str in ["high", "low", "medium"]:
            return ReasoningConfig(
                enable_reasoning=True,
                use_budget_limit=False,
                budget_tokens=None,
                raw_input=reasoning_effort,
            )
    elif reasoning_effort == -1:
        return ReasoningConfig(
            enable_reasoning=True,
            use_budget_limit=False,
            budget_tokens=None,
            raw_input=reasoning_effort,
        )

    tokens = _extract_token_count(reasoning_effort)
    if tokens is not None and tokens > 0:
        return ReasoningConfig(
            enable_reasoning=True,
            use_budget_limit=True,
            budget_tokens=tokens,
            raw_input=reasoning_effort,
        )

    return ReasoningConfig(
        enable_reasoning=ENABLE_THINKING_BUDGET,
        use_budget_limit=ENABLE_THINKING_BUDGET,
        budget_tokens=DEFAULT_THINKING_BUDGET if ENABLE_THINKING_BUDGET else None,
        raw_input=reasoning_effort,
    )


def _extract_token_count(val: Any) -> Optional[int]:

    if isinstance(val, int) and val > 0:
        return val
    if isinstance(val, str):
        try:
            num = int(val.strip())
            return num if num > 0 else None
        except (ValueError, TypeError):
            pass
    return None


def describe_config(cfg: ReasoningConfig) -> str:
    
    if not cfg.enable_reasoning:
        return f"推理模式已停用 (輸入: {cfg.raw_input})"
    if cfg.use_budget_limit and cfg.budget_tokens:
        return f"推理模式啟用，預算上限: {cfg.budget_tokens} tokens (輸入: {cfg.raw_input})"
    return f"推理模式啟用，無預算限制 (輸入: {cfg.raw_input})"
