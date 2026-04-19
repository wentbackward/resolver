"""Validate that all notebooks in tools/analyze/notebooks/ are well-formed
nbformat>=4 documents with no execution outputs baked in."""
from __future__ import annotations

from pathlib import Path

import nbformat
import pytest

NOTEBOOKS_DIR = Path(__file__).parent.parent / "notebooks"
NOTEBOOKS = sorted(NOTEBOOKS_DIR.glob("*.ipynb"))


@pytest.mark.parametrize("nb_path", NOTEBOOKS, ids=[p.name for p in NOTEBOOKS])
def test_notebook_parses(nb_path: Path) -> None:
    """nbformat.read must succeed without raising ValidationError."""
    nb = nbformat.read(str(nb_path), as_version=4)
    assert nb.nbformat >= 4


@pytest.mark.parametrize("nb_path", NOTEBOOKS, ids=[p.name for p in NOTEBOOKS])
def test_notebook_no_outputs(nb_path: Path) -> None:
    """No cell should have execution outputs baked into the committed file."""
    nb = nbformat.read(str(nb_path), as_version=4)
    for i, cell in enumerate(nb.cells):
        if cell.cell_type == "code":
            assert cell.outputs == [], (
                f"{nb_path.name} cell {i} has baked outputs — strip before committing"
            )
            assert cell.execution_count is None, (
                f"{nb_path.name} cell {i} has execution_count != null"
            )
