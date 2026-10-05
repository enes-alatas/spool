-- A connection is private to one loop or shared by the fleet (ADR-0043,
-- #600): owner_loop names the loop a private one belongs to, and NULL is
-- shared. It is no foreign key, because the loop's delete removes its
-- private connections itself, and a column in one could never be dropped.
--
-- What 0042 made of a per-loop secret, or the secrets routes made since,
-- becomes private to its loop: an env-var attached to that loop alone,
-- named as those name one, the loop's name and the variable's, lower case,
-- '_' as '-', cut to 25, then six hex characters. Neither alphabet holds a
-- GLOB character. One the operator has since attached to another loop is
-- shared in fact and stays so, and so does every other connection.
ALTER TABLE connections ADD COLUMN owner_loop TEXT;

UPDATE connections
SET owner_loop = (SELECT loop_id FROM connection_loops WHERE connection = connections.name)
WHERE kind = 'env-var'
  AND (SELECT COUNT(*) FROM connection_loops WHERE connection = connections.name) = 1
  AND EXISTS (
    SELECT 1 FROM connection_loops JOIN loops ON loops.id = connection_loops.loop_id
    WHERE connection_loops.connection = connections.name
      AND connections.name GLOB
          substr(replace(lower(loops.name || '-' || json_extract(connections.config, '$.env')), '_', '-'), 1, 25)
          || '-[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]'
  );
