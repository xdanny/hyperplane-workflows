-- Saved workflows declare the values a person supplies each run, so the same
-- definition can be launched again with new inputs instead of being edited.
-- The shape is the existing template ContextSlot; tokens in the instructions
-- are filled at launch by the same FillSlots substitution templates use.
ALTER TABLE sdk_workflows
    ADD COLUMN inputs JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD CONSTRAINT sdk_workflows_inputs_check CHECK (
        jsonb_typeof(inputs) = 'array' AND jsonb_array_length(inputs) <= 32);
