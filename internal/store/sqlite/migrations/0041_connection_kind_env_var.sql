-- The env-credential kind is named env-var (ADR-0043, #574): not every
-- value a loop reads from an env var is a credential, though every one is
-- kept like a secret.
UPDATE connections SET kind = 'env-var' WHERE kind = 'env-credential';
