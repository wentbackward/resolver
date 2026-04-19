"""Smoke the DB query set against the seeded fixture."""
from __future__ import annotations

from analyze.db import Store


def test_run_summaries_returns_rows(seeded_db):
    with Store(seeded_db) as s:
        runs = s.run_summaries()
    assert len(runs) == 3
    # Sorted by real model, so ModelA runs come before ModelB.
    assert runs[0].resolved_real_model == "Org/ModelA"
    assert runs[-1].resolved_real_model == "Org/ModelB"
    assert runs[-1].overall == "FAIL"


def test_tier_pcts(seeded_db):
    with Store(seeded_db) as s:
        tiers = s.tier_pcts()
    # 3 runs × 2 tiers = 6 rows
    assert len(tiers) == 6
    # run-a1 T1 is 1/1 correct → 100%
    ra1_t1 = [t for t in tiers if t.run_id == "run-a1" and t.tier == "T1"][0]
    assert ra1_t1.pct == 100.0
    # run-b1 T2 is 0/1 correct → 0%
    rb1_t2 = [t for t in tiers if t.run_id == "run-b1" and t.tier == "T2"][0]
    assert rb1_t2.pct == 0.0


def test_variance_only_includes_repeats(seeded_db):
    with Store(seeded_db) as s:
        rows = s.variance()
    # group-a has 2 runs × 2 scenarios = 2 variance rows; group-b is solo so no rows.
    groups = {r.repeat_group for r in rows}
    assert groups == {"group-a"}
    # Both T1.1 and T2.1 were correct in both group-a runs → stddev=0
    assert all(r.stddev_score == 0.0 for r in rows)
    assert all(r.n_runs == 2 for r in rows)


def test_community_for_models(seeded_db):
    with Store(seeded_db) as s:
        rows = s.community_for(["Org/ModelA"])
    assert len(rows) == 1
    assert rows[0].benchmark == "bfcl"


def test_community_empty_returns_nothing(seeded_db):
    with Store(seeded_db) as s:
        assert s.community_for([]) == []
