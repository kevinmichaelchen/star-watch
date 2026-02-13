DEFINE TABLE repo SCHEMAFULL;

DEFINE FIELD owner          ON TABLE repo TYPE string;
DEFINE FIELD name           ON TABLE repo TYPE string;
DEFINE FIELD full_name      ON TABLE repo TYPE string;
DEFINE FIELD description    ON TABLE repo TYPE option<string>;
DEFINE FIELD url            ON TABLE repo TYPE string;
DEFINE FIELD homepage_url   ON TABLE repo TYPE option<string>;
DEFINE FIELD stars          ON TABLE repo TYPE int;
DEFINE FIELD language       ON TABLE repo TYPE option<string>;
DEFINE FIELD topics         ON TABLE repo TYPE array<string>;
DEFINE FIELD readme_excerpt ON TABLE repo TYPE option<string>;
DEFINE FIELD ai_summary     ON TABLE repo TYPE option<string>;
DEFINE FIELD ai_categories  ON TABLE repo TYPE option<array<string>>;
DEFINE FIELD embedding      ON TABLE repo TYPE option<array<float>>;
DEFINE FIELD fetched_at     ON TABLE repo TYPE datetime;
DEFINE FIELD enriched_at    ON TABLE repo TYPE option<datetime>;

DEFINE INDEX idx_full_name ON TABLE repo FIELDS full_name UNIQUE;
DEFINE INDEX idx_hnsw_embedding ON TABLE repo FIELDS embedding HNSW DIMENSION 768 DIST COSINE;
