//go:build duckdb

package aggregate

// schemaVersion tracks breaking changes to the DuckDB schema. v2 aggregator
// ships at schemaVersion=1. Bump and add a migration in migrate() below
// when the shape needs to change.
const schemaVersion = 1

// ddl holds the CREATE statements. Run at first-open; idempotent. Keep
// declarations conservative — `run_config` carries mostly nullable
// fields because v1 manifests have none of them, and HF runs leave
// engine-level columns NULL by design (principle #4: "unknown" is valid).
var ddl = []string{
	`CREATE TABLE IF NOT EXISTS _meta (
		schema_version INTEGER NOT NULL,
		updated_at     TIMESTAMP NOT NULL
	)`,

	`CREATE TABLE IF NOT EXISTS runs (
		run_id              VARCHAR PRIMARY KEY,
		scorecard_path      VARCHAR NOT NULL,
		manifest_path       VARCHAR NOT NULL,
		tier                VARCHAR,
		sweep               VARCHAR,
		model               VARCHAR,
		resolved_real_model VARCHAR,
		endpoint            VARCHAR,
		adapter             VARCHAR,
		tokenizer_mode      VARCHAR,
		manifest_version    INTEGER,
		started_at          TIMESTAMP,
		finished_at         TIMESTAMP,
		go_version          VARCHAR,
		commit_sha          VARCHAR,
		host_name           VARCHAR,
		overall             VARCHAR,
		total_ms            BIGINT,
		avg_ms              BIGINT,
		p50_ms              BIGINT,
		p95_ms              BIGINT,
		max_ms              BIGINT,
		query_count         INTEGER,
		correct_count       INTEGER,
		partial_count       INTEGER,
		incorrect_count     INTEGER,
		error_count         INTEGER
	)`,

	`CREATE TABLE IF NOT EXISTS queries (
		run_id          VARCHAR NOT NULL,
		tier            VARCHAR,
		scenario_id     VARCHAR NOT NULL,
		query           VARCHAR,
		expected_tool   VARCHAR,
		score           VARCHAR,
		reason          VARCHAR,
		elapsed_ms      BIGINT,
		tool_calls_json VARCHAR,
		content         VARCHAR,
		PRIMARY KEY (run_id, scenario_id)
	)`,

	`CREATE TABLE IF NOT EXISTS sweep_rows (
		run_id                  VARCHAR NOT NULL,
		axis_value              INTEGER,
		seed                    INTEGER,
		score                   VARCHAR,
		tools_called            INTEGER,
		wrong_tool_count        INTEGER,
		hallucinated_tool_count INTEGER,
		needle_found            BOOLEAN,
		accuracy                DOUBLE,
		context_tokens          INTEGER,
		elapsed_ms              BIGINT,
		completed               BOOLEAN
	)`,

	// run_config: all columns nullable. v1 manifests without a sidecar land
	// in runs but contribute zero rows here; v2 manifests with a sidecar
	// contribute one row per run_id.
	`CREATE TABLE IF NOT EXISTS run_config (
		run_id                     VARCHAR PRIMARY KEY,
		virtual_model              VARCHAR,
		real_model                 VARCHAR,
		backend                    VARCHAR,
		backend_port               INTEGER,
		default_temperature        DOUBLE,
		default_top_p              DOUBLE,
		default_top_k              INTEGER,
		default_presence_penalty   DOUBLE,
		default_frequency_penalty  DOUBLE,
		default_max_tokens         INTEGER,
		default_enable_thinking    BOOLEAN,
		clamp_enable_thinking      BOOLEAN,
		container                  VARCHAR,
		tensor_parallel            INTEGER,
		gpu_memory_utilization     DOUBLE,
		context_size               INTEGER,
		max_num_batched_tokens     INTEGER,
		kv_cache_dtype             VARCHAR,
		attention_backend          VARCHAR,
		prefix_caching             BOOLEAN,
		enable_auto_tool_choice    BOOLEAN,
		tool_parser                VARCHAR,
		reasoning_parser           VARCHAR,
		chat_template              VARCHAR,
		mtp                        BOOLEAN,
		mtp_method                 VARCHAR,
		mtp_num_speculative_tokens INTEGER,
		load_format                VARCHAR,
		quantization               VARCHAR,
		captured_at                VARCHAR,
		proxy_recipe_path          VARCHAR,
		vllm_recipe_path           VARCHAR,
		repeat_group               VARCHAR,
		notes                      VARCHAR
	)`,

	// community_benchmarks: truncate-and-reloaded from YAML on every
	// aggregate. The YAML itself is append-only; this table is a derived
	// mirror per v2 plan.
	// model_key is the NormalizeModel() output stored alongside the raw
	// model name so joins against resolved_real_model are fast and exact
	// without requiring a UDF at query time.
	`CREATE TABLE IF NOT EXISTS community_benchmarks (
		model      VARCHAR NOT NULL,
		model_key  VARCHAR NOT NULL,
		benchmark  VARCHAR NOT NULL,
		metric     VARCHAR NOT NULL,
		value      DOUBLE NOT NULL,
		source_url VARCHAR NOT NULL,
		as_of      DATE NOT NULL,
		notes      VARCHAR,
		PRIMARY KEY (model, benchmark, metric)
	)`,
}

// viewDDL is re-created on every open so schema evolution doesn't leave
// stale view definitions.
var viewDDL = []string{
	`CREATE OR REPLACE VIEW comparison AS
	 SELECT
	   r.run_id, r.tier, r.model, r.resolved_real_model, r.overall,
	   r.total_ms AS run_total_ms,
	   q.scenario_id, q.score, q.elapsed_ms,
	   c.real_model                 AS cfg_real_model,
	   c.default_enable_thinking    AS cfg_thinking,
	   c.tool_parser                AS cfg_tool_parser,
	   c.reasoning_parser           AS cfg_reasoning_parser,
	   c.mtp                        AS cfg_mtp,
	   c.context_size               AS cfg_context_size,
	   c.quantization               AS cfg_quantization
	 FROM runs r
	 JOIN queries q ON q.run_id = r.run_id
	 LEFT JOIN run_config c ON c.run_id = r.run_id`,

	`CREATE OR REPLACE VIEW run_summary AS
	 SELECT
	   r.run_id, r.model, r.resolved_real_model, r.overall,
	   r.correct_count, r.partial_count, r.incorrect_count, r.error_count,
	   r.query_count, r.total_ms, r.p95_ms,
	   c.real_model AS cfg_real_model, c.default_enable_thinking AS cfg_thinking,
	   c.tool_parser, c.mtp, c.context_size, c.quantization
	 FROM runs r
	 LEFT JOIN run_config c ON c.run_id = r.run_id`,
}
