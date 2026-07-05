-- title 的 ILIKE 過濾目前靠 seq scan;資料量大時可加 pg_trgm GIN 索引
CREATE TABLE events (
    id              BIGSERIAL PRIMARY KEY,
    source          TEXT        NOT NULL,
    source_event_id TEXT        NOT NULL,
    title           TEXT        NOT NULL,
    url             TEXT        NOT NULL,
    venue           TEXT        NOT NULL DEFAULT '',
    start_time      TIMESTAMPTZ,
    on_sale_time    TIMESTAMPTZ,
    raw             JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source, source_event_id)
);
