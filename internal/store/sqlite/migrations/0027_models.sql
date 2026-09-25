-- The model list's own state (ADR-0033, #332). model_resolutions says what a
-- model name runs as on this hub: a family alias or a custom entry, resolved
-- by the hub's own run ('probe') or seen at a real turn's init ('turn').
CREATE TABLE model_resolutions (
    model       TEXT PRIMARY KEY,
    resolved    TEXT NOT NULL,
    source      TEXT NOT NULL,
    cli_version TEXT NOT NULL DEFAULT '',
    resolved_at INTEGER NOT NULL
);

-- The operator's extra entries, offered after the aliases in both dropdowns.
CREATE TABLE custom_models (
    id         TEXT PRIMARY KEY,
    model      TEXT NOT NULL UNIQUE,
    label      TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);
