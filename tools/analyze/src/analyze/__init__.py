"""resolver-analyze: cross-run analyzer for the resolver benchmark.

Reads DuckDB produced by `resolver aggregate` and the community-benchmarks
YAML, runs a fixed query set, and optionally asks an LLM to author an
opinionated Markdown report. Go produces, Python consumes — this package
never writes under `reports/`.
"""

__version__ = "0.1.0"
