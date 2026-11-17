-- Organizational identity, separate from the SDK's execution fingerprint.
-- One projection serves admission and both project read models, including runs
-- created before source associations were recorded. No history rewrite is needed.
CREATE FUNCTION project_workflow_identity(options jsonb, dag text, goal text, doc jsonb)
RETURNS jsonb LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE
    identity jsonb := options->'projectWorkflow';
    request jsonb;
    definition jsonb;
    definition_key text;
    source_id text;
    display_name text;
BEGIN
    IF jsonb_typeof(identity) = 'object'
       AND identity->>'version' = '1'
       AND NULLIF(identity->>'key', '') IS NOT NULL THEN
        RETURN identity;
    END IF;
    request := options->'request';
    source_id := NULLIF(options->>'sourceWorkflowId', '');
    IF options->>'runtime' = 'codex-sdk' AND jsonb_typeof(request) = 'object'
       AND request->>'workflow' IN ('node-workflow', 'checked-task', 'app-ideator') THEN
        definition := jsonb_build_object(
            'workflow', request->>'workflow',
            'instruction', COALESCE(request->>'instruction', ''),
            'domain', COALESCE(request->>'domain', ''),
            'nodes', COALESCE(NULLIF(request->'nodes', 'null'::jsonb), '[]'::jsonb),
            'check', COALESCE(NULLIF(request->'check', 'null'::jsonb), '[]'::jsonb),
            'autoRepair', COALESCE(request->'autoRepair', 'false'::jsonb),
            'target', CASE WHEN request->>'workflow' = 'checked-task' THEN request->>'workspace' ELSE '' END);
        definition_key := 'definition:v1:' || encode(sha256(convert_to(definition::text, 'UTF8')), 'hex');
        display_name := COALESCE(NULLIF(btrim(options->>'workflowName'), ''),
            NULLIF(btrim(goal), ''), NULLIF(btrim(request->>'instruction'), ''),
            NULLIF(btrim(request->>'domain'), ''), dag, 'Workflow');
        RETURN jsonb_strip_nulls(jsonb_build_object('version', 1,
            'key', CASE WHEN source_id IS NULL THEN definition_key ELSE 'saved:' || source_id END,
            'name', left(display_name, 160), 'definitionKey', definition_key, 'sourceWorkflowId', source_id));
    END IF;
    -- Preserve the previous grouping for legacy display records only.
    RETURN jsonb_build_object('version', 1,
        'key', 'legacy:' || encode(sha256(convert_to(jsonb_build_array(dag, COALESCE(doc->>'recipe', ''))::text, 'UTF8')), 'hex'),
        'name', COALESCE(NULLIF(dag, ''), 'Workflow'));
END;
$$;
