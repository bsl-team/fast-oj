-- 0004: site settings, editable in the browser instead of by restarting.
-- Values are strings keyed by name; the typed view lives in Go (SiteConfig).

CREATE TABLE site_settings (
    key        TEXT    PRIMARY KEY,
    value      TEXT    NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL,
    updated_by INTEGER REFERENCES users(id) ON DELETE SET NULL
);

INSERT INTO site_settings (key, value, updated_at) VALUES
    ('site.title',    'fast-oj',        0),
    ('site.tagline',  '极简、极快的在线判题。', 0),
    ('site.footer',   '',               0),
    ('site.links',    '[]',             0),
    ('site.allow_register', '1',        0);
