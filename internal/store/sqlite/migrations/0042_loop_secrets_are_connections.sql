-- Per-loop secrets become connections (ADR-0043, #576). Each loop secret
-- is an env-var connection on the same variable, holding the same value,
-- attached to its loop only, so no loop's env changes. Its name joins the
-- loop's and the variable's in the connection alphabet: lower case, '_'
-- as '-', cut to 25, then six random hex characters, because a loop name
-- may hold '_' and two pairs could otherwise meet on one name. An env-var
-- the operator attached on the same variable is detached first: until
-- now the per-loop secret was what the loop's env held, and leaving both
-- would let either set it.
ALTER TABLE connections ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0;
UPDATE connections SET updated_at = created_at;

CREATE TEMP TABLE moved_secrets AS
SELECT substr(replace(lower(loops.name || '-' || loop_secrets.name), '_', '-'), 1, 25)
           || '-' || lower(hex(randomblob(3))) AS connection,
       loop_secrets.loop_id AS loop_id,
       loop_secrets.name AS env,
       loop_secrets.value AS value,
       loop_secrets.updated_at AS updated_at
FROM loop_secrets JOIN loops ON loops.id = loop_secrets.loop_id;

DELETE FROM connection_loops
WHERE EXISTS (
    SELECT 1 FROM moved_secrets JOIN connections ON connections.name = connection_loops.connection
    WHERE moved_secrets.loop_id = connection_loops.loop_id
      AND connections.kind = 'env-var'
      AND json_extract(connections.config, '$.env') = moved_secrets.env
);

INSERT INTO connections (name, kind, config, secret, created_at, updated_at)
SELECT connection, 'env-var', json_object('env', env), value, updated_at, updated_at FROM moved_secrets;
INSERT INTO connection_loops (connection, loop_id, attached_at)
SELECT connection, loop_id, updated_at FROM moved_secrets;

DROP TABLE moved_secrets;
DROP TABLE loop_secrets;
