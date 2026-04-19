"""Pin tests for _normalize_model — mirrors the Go TestNormalizeModel cases.

The Go and Python implementations must produce identical output for the same
inputs. If they diverge, community_for() joins will silently miss rows.
"""
from __future__ import annotations

import pytest

from analyze.db import _normalize_model


# ---------------------------------------------------------------------------
# Equivalence: these three forms must all produce the same key.
# ---------------------------------------------------------------------------

EQUIV_CASES = [
    "Qwen/Qwen3.6-35B-A3B-FP8",
    "Qwen3.6-35B-A3B",
    "qwen3.6-35b-a3b",
]
EQUIV_WANT = "qwen3.6-35b-a3b"


@pytest.mark.parametrize("name", EQUIV_CASES)
def test_equivalence(name: str) -> None:
    assert _normalize_model(name) == EQUIV_WANT


def test_non_match_different_version() -> None:
    """Qwen3.5 must NOT normalise to the same key as Qwen3.6."""
    result = _normalize_model("Qwen3.5-35B-A3B")
    assert result != EQUIV_WANT, f"expected different key, got {result!r}"


def test_fp16_suffix_stripped() -> None:
    assert _normalize_model("Meta/Llama-3.1-8B-FP16") == "llama-3.1-8b"


def test_multi_suffix_stripped() -> None:
    """Stacked suffixes like -INT4-AWQ both get stripped."""
    assert _normalize_model("some/Model-INT4-AWQ") == "model"


def test_separator_collapse() -> None:
    assert _normalize_model("qwen3.6-35b__a3b") == "qwen3.6-35b-a3b"


@pytest.mark.parametrize(
    "name",
    [
        "Qwen/Qwen3.6-35B-A3B-FP8",
        "qwen3.6-35b-a3b",
        "Meta/Llama-3.1-8B-FP16",
        "some/Model-INT4-AWQ",
        "plain-model-name",
        "Org/Model.Name_v2-GPTQ",
    ],
)
def test_idempotent(name: str) -> None:
    once = _normalize_model(name)
    twice = _normalize_model(once)
    assert once == twice, f"not idempotent: {name!r} → {once!r} → {twice!r}"


# ---------------------------------------------------------------------------
# Fixture parity: same inputs must produce same outputs as Go golden values.
# ---------------------------------------------------------------------------

GO_GOLDEN = [
    # (input, expected_output)  — verified against Go TestNormalizeModel
    ("Qwen/Qwen3.6-35B-A3B-FP8", "qwen3.6-35b-a3b"),
    ("Qwen3.6-35B-A3B", "qwen3.6-35b-a3b"),
    ("qwen3.6-35b-a3b", "qwen3.6-35b-a3b"),
    ("Meta/Llama-3.1-8B-FP16", "llama-3.1-8b"),
    ("some/Model-INT4-AWQ", "model"),
    ("qwen3.6-35b__a3b", "qwen3.6-35b-a3b"),
]


@pytest.mark.parametrize("name,want", GO_GOLDEN)
def test_matches_go_golden(name: str, want: str) -> None:
    """Python output must match the Go golden values to prevent join drift."""
    assert _normalize_model(name) == want, (
        f"_normalize_model({name!r}) = {_normalize_model(name)!r}, "
        f"Go produces {want!r}"
    )
