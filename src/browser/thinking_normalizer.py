from dataclasses import dataclass
from typing import Any, Optional, Union

from config import DEFAULT_THINKING_BUDGET, ENABLE_THINKING_BUDGET


@dataclass
class ReasoningConfig:
    enable_reasoning: bool
    use_budget_limit: bool
    budget_tokens: Optional[int]
    raw_input: Any


def parse_reasoning_param(effort: Optional[Union[int, str]]) -> ReasoningConfig:
    if effort is None:
        return ReasoningConfig(
            enable_reasoning=ENABLE_THINKING_BUDGET,
            use_budget_limit=ENABLE_THINKING_BUDGET,
            budget_tokens=DEFAULT_THINKING_BUDGET if ENABLE_THINKING_BUDGET else None,
            raw_input=None,
        )

    if effort == 0 or (isinstance(effort, str) and effort.strip() == "0"):
        return ReasoningConfig(
            enable_reasoning=False,
            use_budget_limit=False,
            budget_tokens=None,
            raw_input=effort,
        )

    if isinstance(effort, str):
        val = effort.strip().lower()
        if val in ["none", "-1"]:
            return ReasoningConfig(
                enable_reasoning=True,
                use_budget_limit=False,
                budget_tokens=None,
                raw_input=effort,
            )
        level_map = {"low": 4096, "medium": 8192, "high": 16384}
        if val in level_map:
            return ReasoningConfig(
                enable_reasoning=True,
                use_budget_limit=True,
                budget_tokens=level_map[val],
                raw_input=effort,
            )
    elif effort == -1:
        return ReasoningConfig(
            enable_reasoning=True,
            use_budget_limit=False,
            budget_tokens=None,
            raw_input=effort,
        )

    tokens = _parse_token_count(effort)
    if tokens is not None and tokens > 0:
        return ReasoningConfig(
            enable_reasoning=True,
            use_budget_limit=True,
            budget_tokens=tokens,
            raw_input=effort,
        )

    return ReasoningConfig(
        enable_reasoning=ENABLE_THINKING_BUDGET,
        use_budget_limit=ENABLE_THINKING_BUDGET,
        budget_tokens=DEFAULT_THINKING_BUDGET if ENABLE_THINKING_BUDGET else None,
        raw_input=effort,
    )


def _parse_token_count(val: Any) -> Optional[int]:
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
