-- A loop's runtime can be cloud: a Claude Code cloud session (ADR-0056).
-- SQLite cannot alter a column's CHECK, and rebuilding loops would drop it
-- under every table that cascades from it. Widening a CHECK changes no row
-- on disk, so the stored schema is edited in place, as SQLite documents for
-- such a change. A table created and dropped after the edit moves the
-- schema cookie on, so a connection holding the old schema reloads it.
PRAGMA writable_schema = ON;
UPDATE sqlite_schema
   SET sql = replace(sql,
       'CHECK (runtime IN (''bare'',''docker''))',
       'CHECK (runtime IN (''bare'',''docker'',''cloud''))')
 WHERE type = 'table' AND name = 'loops';
PRAGMA writable_schema = RESET;
-- An edit that matched nothing fails the migration here, rather than as a
-- refused write the first time a cloud loop is stored.
CREATE TABLE schema_cookie_bump (widened INTEGER CHECK (widened));
INSERT INTO schema_cookie_bump
SELECT instr(sql, 'CHECK (runtime IN (''bare'',''docker'',''cloud''))') > 0
  FROM sqlite_schema WHERE type = 'table' AND name = 'loops';
DROP TABLE schema_cookie_bump;
