-- What the CLI said when the API did not recognize a loop's model (#289):
-- a sentence naming the model, '' while it has not been refused. A loop with
-- one takes no turns until its model is edited, and the edit clears it.
ALTER TABLE loops ADD COLUMN model_refusal TEXT NOT NULL DEFAULT '';
