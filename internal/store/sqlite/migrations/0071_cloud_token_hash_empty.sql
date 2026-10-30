-- A cloud loop created with no hub token was written with the SHA-256 of
-- the empty string as its token's hash (#1213): it read as holding a token
-- it never had, and the unique index on the hash refused a second such
-- loop. No token hashes to it, so the hash is cleared.
UPDATE loops SET hub_mcp_token_hash = ''
WHERE hub_mcp_token_hash = 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855';
