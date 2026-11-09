-- A fleet rule's scope (ADR-0066, #1436): NULL binds every loop on
-- the hub, as every rule before this column does; a team's name binds that
-- team's loops only. A rename follows into the rule, and deleting the team
-- deletes its rules (ADR-0066). A column added with a reference may
-- default only to NULL.
ALTER TABLE fleet_rules ADD COLUMN team TEXT REFERENCES teams(name) ON UPDATE CASCADE ON DELETE CASCADE;
