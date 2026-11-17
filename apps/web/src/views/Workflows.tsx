import { useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { Link, useLocation } from 'react-router-dom';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../api';
import { WorkflowProposal } from '../components/WorkflowProposal';
import { WorkflowNodes } from '../components/WorkflowNodes';
import { nodeTextIssue, textByteIssue } from '../workflowValidation';
import { useWorkflowValidation } from '../hooks/useWorkflowValidation';
import { WorkflowValidationStatus } from '../components/WorkflowValidationStatus';
import { WorkflowTemplates } from '../components/WorkflowTemplates';
import { WorkflowInputs } from '../components/WorkflowInputs';
import { WorkflowSignOff } from '../components/WorkflowSignOff';
import { WorkflowRunInputs, inputAnswered } from '../components/WorkflowRunInputs';
import { WorkflowInputInsert, WorkflowUsesInputs } from '../components/WorkflowInputTokens';
import { initialInputValues, inputIssue, missingInputs, rememberInputs, unknownInputs } from '../workflowInputs';
import { stepKindSummary } from '../workflowSummary';
import { readSignOff, WorkflowInputError } from '../api';
import type { ContextSlot, SDKWorkflow, SDKWorkflowNode, SignOffPolicy } from '../api';
import { ProjectPicker } from '../components/ProjectPicker';
import { useSelectedRun } from '../hooks/useSelectedRun';
import { useProjectScope } from '../hooks/useProjectScope';
import './Workflows.css';

// All authoring paths save reviewed settings and launch through the SDK.
export function Workflows() {
  const location = useLocation();
  // `setup` is a definition to edit; `slots` are the run values it came with —
  // a previous run's inputs, or the values typed into a template. They are not
  // part of the definition and are never written into its instructions.
  const handoff = location.state as { brief?: string; project?: string; setup?: SDKWorkflow; templateId?: string; slots?: Record<string, string> } | null;
  const setup = handoff?.setup;
  const scope = useProjectScope((state) => state.project);
  const { setRunId } = useSelectedRun();
  const queryClient = useQueryClient();
  const [mode, setMode] = useState<'nodes' | 'task' | 'ideator'>(setup?.kind === 'task' ? 'task' : setup?.kind === 'app-ideator' ? 'ideator' : 'nodes');
  const [nodes, setNodes] = useState<SDKWorkflowNode[]>(setup?.nodes ?? []);
  const [inputs, setInputs] = useState<ContextSlot[]>(setup?.inputs ?? []);
  const [slotValues, setSlotValues] = useState<Record<string, string>>(initialInputValues(setup?.id ?? '', handoff?.slots));
  const [fillOpen, setFillOpen] = useState(false);
  const [pendingProject, setPendingProject] = useState('');
  const [namePlaceholder, setNamePlaceholder] = useState('');
  const [instruction, setInstruction] = useState(setup?.instruction ?? handoff?.brief ?? '');
  const [project, setProject] = useState(setup?.project ?? handoff?.project ?? scope ?? '');
  const [target, setTarget] = useState(setup?.targetRoot ?? '');
  const [extraCheck, setExtraCheck] = useState(Boolean(setup?.check.length));
  const [program, setProgram] = useState(setup?.check[0] ?? '');
  const [argumentsText, setArgumentsText] = useState(setup?.check.slice(1).join('\n') ?? '');
  const [loadedArguments, setLoadedArguments] = useState<string[] | null>(setup?.check.slice(1) ?? null);
  const [domain, setDomain] = useState(setup?.kind === 'app-ideator' ? setup.instruction : '');
  const [correction, setCorrection] = useState(setup?.autoRepair ?? false);
  // What this workflow asks of a person when a run of it finishes (M-11). A
  // definition that predates the setting reads as "required", which is what
  // every workflow did before the setting existed.
  const [signOff, setSignOff] = useState<SignOffPolicy>(readSignOff(setup?.signOff));
  // A workflow handed over from its own history page is a SAVED workflow, not a
  // fresh draft: it arrives with an id, a revision and a name. Every other
  // field of `setup` was read above and these two were not, so "Run again with
  // the same inputs" opened an editor that had the steps and the values but no
  // identity — and therefore demanded "Add a workflow name to save" and refused
  // to launch by reference for a workflow that was already saved (D-11).
  //
  // `setup.id` is the test, because the run page hands over a setup with an
  // empty id when a run had no saved source; that one really is a draft.
  const [saved, setSaved] = useState<SDKWorkflow | null>(setup?.id ? setup : null);
  const [name, setName] = useState(setup?.id ? setup.name : '');
  const nameInput = useRef<HTMLInputElement>(null);
  const saveError = useRef<HTMLParagraphElement>(null);
  const launchError = useRef<HTMLParagraphElement>(null);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [preparing, setPreparing] = useState(false);
  const checkCatalog = useQuery({ queryKey: ['workflow-checks'], queryFn: api.workflowChecks, retry: false });
  const checks = checkCatalog.error ? undefined : checkCatalog.data;
  const library = useQuery({ queryKey: ['workflows'], queryFn: api.workflows });
  const settings: SDKWorkflow = { id: saved?.id ?? '', revision: saved?.revision ?? 0, name: name.trim(),
    kind: mode === 'ideator' ? 'app-ideator' : mode, ...(mode === 'nodes' ? { nodes, inputs } : {}), instruction: (mode === 'ideator' ? domain : instruction).trim(),
    targetRoot: mode === 'task' ? target.trim() : '',
    check: mode === 'task' && extraCheck ? [program.trim(), ...(loadedArguments ?? argumentsText.split(/\r?\n/).filter((line) => line.length > 0))] : [], project, autoRepair: correction, signOff };
  const load = (workflow: SDKWorkflow, resetMutations = true, values?: Record<string, string>) => {
    setSaved(workflow.id ? workflow : null); setName(workflow.name); setMode(workflow.kind === 'app-ideator' ? 'ideator' : workflow.kind); setNodes(workflow.nodes ?? []);
    setInputs(workflow.inputs ?? []); setFillOpen(false);
    setSlotValues(initialInputValues(workflow.id, values));
    setInstruction(workflow.kind !== 'app-ideator' ? workflow.instruction : ''); setDomain(workflow.kind === 'app-ideator' ? workflow.instruction : '');
    setExtraCheck(Boolean(workflow.check.length)); setTarget(workflow.targetRoot); setProgram(workflow.check[0] ?? ''); setArgumentsText(workflow.check.slice(1).join('\n'));
    setLoadedArguments(workflow.check.slice(1));
    setProject(workflow.project); setCorrection(workflow.autoRepair); setSignOff(readSignOff(workflow.signOff)); setConfirmRemove(false);
    if (resetMutations) { save.reset(); remove.reset(); launch.reset(); }
  };
  const refresh = () => { void queryClient.invalidateQueries({ queryKey: ['workflows'] }); };
  const save = useMutation({ mutationFn: () => api.saveWorkflow(settings), onSuccess: (workflow) => { load(workflow, false); refresh(); }, onError: refresh });
  const remove = useMutation({ mutationFn: () => api.removeWorkflow(saved!), onSuccess: () => { setSaved(null); setConfirmRemove(false); save.reset(); refresh(); }, onError: refresh });
  const launchSource = { sourceWorkflowId: saved?.id, workflowName: settings.name };
  const unchangedSave = saved != null && Object.entries(settings).every(([key, value]) => JSON.stringify(value) === JSON.stringify(saved[key as keyof SDKWorkflow]));
  // A workflow that asks for inputs is launched BY REFERENCE: the server loads
  // the saved definition, fills the tokens and validates. The browser never
  // fills them, so the launched definition is provably the saved one — which is
  // why an edited or unsaved definition cannot be run with inputs at all.
  const declaredInputs = mode === 'nodes' ? inputs : [];
  const byReference = declaredInputs.length > 0 && Boolean(saved?.id) && unchangedSave;
  const launch = useMutation({
    mutationFn: () => byReference
      ? api.startRun({ sourceWorkflowId: saved!.id, revision: saved!.revision, slots: Object.fromEntries(declaredInputs.map(input => [input.id, slotValues[input.id] ?? ''])) })
      : mode === 'nodes'
      ? api.startRun({ ...launchSource, nodes, goal: settings.instruction, project, autoRepair: correction })
      : mode === 'ideator'
      ? api.startRun({ ...launchSource, workflow: 'app-ideator', inputs: { domain: domain.trim() }, project, autoRepair: correction })
      : api.startRun({ ...launchSource, task: settings.instruction, targetRoot: settings.targetRoot,
        check: settings.check,
        project, autoRepair: correction }),
    onSuccess: ({ runId }) => {
      if (byReference && saved?.id) rememberInputs(saved.id, slotValues);
      void queryClient.invalidateQueries({ queryKey: ['runs'] }); setRunId(runId);
    },
  });
  const goalIssue = textByteIssue(settings.instruction, mode === 'ideator' ? 'the idea description' : 'the goal', mode === 'ideator' ? 4000 : 16000);
  const nameIssue = textByteIssue(settings.name, 'the workflow name', 160);
  const validation = useWorkflowValidation(mode === 'nodes' && instruction.trim() && nodes.length > 0 && !goalIssue && nodes.every(node => !nodeTextIssue(node))
    ? { instruction: settings.instruction, nodes } : undefined);
  const requirementsAvailable = nodes.every(node => !node.acceptance || checks?.some(check => check.id === node.acceptance));
  const unknownTokens = mode === 'nodes' ? unknownInputs(inputs, settings.instruction, nodes) : [];
  const serverUnknown = save.error instanceof WorkflowInputError ? save.error.unknownInputs : launch.error instanceof WorkflowInputError ? launch.error.unknownInputs : [];
  const declaredIssue = inputs.some(input => inputIssue(input, inputs));
  const inputsReady = !unknownTokens.length && !declaredIssue;
  const ready = !goalIssue && !nameIssue && (mode === 'nodes' ? validation.valid && requirementsAvailable && inputsReady : mode === 'ideator' ? Boolean(domain.trim()) : Boolean(instruction.trim() && target.trim() && (!extraCheck || program.trim())));
  const busy = launch.isPending || save.isPending || remove.isPending || preparing;
  const unchanged = unchangedSave;
  const serverMissing = launch.error instanceof WorkflowInputError ? launch.error.missingSlots : [];
  const missing = missingInputs(declaredInputs, slotValues, inputAnswered);
  const needsSaveToRun = declaredInputs.length > 0 && !byReference;
  // Every reason Save or Run is unavailable is named where the buttons are, and
  // each one moves focus to the field that answers it. A disabled control with
  // no reason is the defect this replaces.
  const reasons: { hint: string; focus: () => void }[] = [
    ...(!settings.instruction ? [{ hint: mode === 'ideator' ? 'Add an idea description to save' : 'Add a goal to save', focus: () => document.getElementById(mode === 'nodes' ? 'workflow-goal' : mode === 'task' ? 'task-goal' : 'idea-domain')?.focus() }] : []),
    ...(mode === 'nodes' && !nodes.length ? [{ hint: 'Add a step to save', focus: () => document.querySelector<HTMLElement>('.workflow-palette button')?.focus() }] : []),
    ...(!name.trim() || nameIssue ? [{ hint: nameIssue ? 'Review workflow name' : 'Add a workflow name to save', focus: () => nameInput.current?.focus() }] : []),
    ...(mode === 'nodes' && validation.issue ? [{ hint: 'Fix the step marked above', focus: () => document.getElementById(`title-${validation.issue?.nodeId ?? ''}`)?.focus() ?? document.getElementById('workflow-goal')?.focus() }] : []),
    ...(unknownTokens.length ? [{ hint: 'Declare the inputs used by the steps', focus: () => document.getElementById('workflow-inputs-title')?.scrollIntoView({ block: 'nearest' }) }] : []),
    ...(declaredIssue ? [{ hint: 'Finish the input marked above', focus: () => document.getElementById('input-label-0')?.focus() }] : []),
    ...(pendingProject ? [{ hint: 'Create the project first', focus: () => document.querySelector<HTMLElement>('.projpick-new-input')?.focus() }] : []),
  ];
  const blocked = Boolean(pendingProject);
  const submit = (event: FormEvent) => { event.preventDefault(); if (ready && !busy && !blocked && !missing.length) launch.mutate(); };

  return (
    <div className="sdk-start">
      <header className="sdk-start-header">
        <div><h1>Workflows</h1><p>Build the steps, save your workflow, and review the results.</p></div>
        <Link to="/runs" className="btn">View runs</Link>
      </header>
      <section className="sdk-library" aria-labelledby="saved-workflows-title">
        <div className="sdk-library-header"><h2 id="saved-workflows-title">Saved workflows</h2>
          <button className="btn" disabled={busy} onClick={() => load({ id: '', revision: 0, name: '', kind: 'nodes', nodes: [], instruction: '', targetRoot: '', check: [], project: scope ?? '', autoRepair: false })}>New workflow</button></div>
        {library.isPending ? <p>Loading workflows…</p> : library.error ? <p role="alert">Could not load saved workflows. <button className="btn" onClick={() => void library.refetch()}>Retry</button></p>
          : library.data?.length === 0 ? <p className="sdk-field-hint">Start below, then save the setup to reuse it. Saving does not start a run.</p>
          : <ul className="sdk-workflow-list">{library.data?.map((workflow) => <li key={workflow.id}><button className="sdk-workflow-choice" aria-pressed={saved?.id === workflow.id} disabled={busy} onClick={() => load(workflow)}>
              <strong>{workflow.name}</strong><span>{workflow.instruction}</span><small>{workflow.kind === 'task' ? workflow.targetRoot : workflow.kind === 'nodes'
                ? [`${workflow.nodes?.length ?? 0} ${(workflow.nodes?.length ?? 0) === 1 ? 'step' : 'steps'}`, stepKindSummary(workflow.nodes ?? []),
                   ...(workflow.inputs?.length ? [`asks for ${workflow.inputs.length} ${workflow.inputs.length === 1 ? 'input' : 'inputs'}`] : [])].filter(Boolean).join(' · ')
                : 'App ideas · built-in checks'}</small>
            </button>{workflow.kind === 'nodes' && <Link className="sdk-workflow-history" to={`/workflows/${encodeURIComponent(workflow.id)}`}>Runs of this workflow →</Link>}</li>)}</ul>}
      </section>
      <WorkflowTemplates checks={checks} initialId={handoff?.templateId} project={project} disabled={busy} hasSteps={nodes.length > 0 || mode !== 'nodes'}
        onUse={(workflow, prepared) => { load({ ...workflow, name: '' }, true, prepared.slots); setNamePlaceholder(prepared.title); }} onPreparing={setPreparing} />
      <div className="sdk-start-choices" aria-label="Work type">
        <button className={`btn ${mode === 'nodes' ? 'btn--primary' : ''}`} aria-pressed={mode === 'nodes'} disabled={busy}
          onClick={() => { setMode('nodes'); launch.reset(); }}>Build with steps</button>
        <button className={`btn ${mode === 'task' ? 'btn--primary' : ''}`} aria-pressed={mode === 'task'} disabled={busy}
          onClick={() => { setMode('task'); launch.reset(); }}>Project task</button>
        <button className={`btn ${mode === 'ideator' ? 'btn--primary' : ''}`} aria-pressed={mode === 'ideator'} disabled={busy}
          onClick={() => { setMode('ideator'); launch.reset(); }}>App ideas</button>
      </div>
      <form onSubmit={submit} className="panel sdk-start-form">
        <fieldset disabled={busy}>
          {mode === 'nodes' ? <>
            <div className="workflow-instruction-heading">
              <label htmlFor="workflow-goal">What do you want to achieve?</label>
              <WorkflowInputInsert inputs={inputs} target="workflow-goal" describes="the goal" disabled={busy} onInsert={setInstruction} />
            </div>
            <textarea id="workflow-goal" className="input sdk-goal" rows={3} required maxLength={16000} value={instruction}
              placeholder="Plan an easy first version of an app for weekend cyclists" onChange={event => setInstruction(event.target.value)} />
            <WorkflowUsesInputs inputs={inputs} text={instruction} />
            {goalIssue && <p role="alert">{goalIssue}</p>}
            <WorkflowProposal checks={checks} goal={instruction} project={project} count={nodes.length} disabled={busy} onUse={workflow => { setNodes(workflow.nodes ?? []); if (workflow.instruction) setInstruction(workflow.instruction); if (workflow.inputs?.length) setInputs(workflow.inputs); if (!name.trim()) setNamePlaceholder(workflow.name); }} />
            {checkCatalog.isPending ? <p role="status">Loading optional check requirements…</p> : checkCatalog.error ? <p role="alert">Could not load extra check requirements. Existing selections are preserved; workflows using them cannot be saved or started. <button type="button" className="btn" onClick={() => void checkCatalog.refetch()}>Retry check catalog</button></p> : null}
            <WorkflowValidationStatus validation={validation} showIssue={!validation.issue?.nodeId || !nodes.some(node => node.id === validation.issue?.nodeId)} />
            <WorkflowInputs inputs={inputs} nodes={nodes} instruction={instruction} disabled={busy}
              unknown={unknownTokens.length ? unknownTokens : serverUnknown} onChange={setInputs} />
            <WorkflowNodes instruction={instruction} checks={checks} issue={validation.issue} nodes={nodes} onChange={setNodes} disabled={busy} inputs={inputs} />
          </> : mode === 'task' ? <>
            <label htmlFor="task-goal">What should change?</label>
            <textarea id="task-goal" className="input sdk-goal" rows={5} required value={instruction}
              placeholder="Fix the counter so a click or Enter adds exactly one session."
              onChange={(event) => setInstruction(event.target.value)} />
            {goalIssue && <p role="alert">{goalIssue}</p>}
            <label htmlFor="task-folder">Working folder</label>
            <input id="task-folder" className="input mono" required value={target} placeholder="/absolute/path/to/project"
              aria-describedby="task-folder-hint" onChange={(event) => setTarget(event.target.value)} />
            <p id="task-folder-hint" className="sdk-field-hint">An existing folder on the server. The worker can edit this folder; network access and tool installation are disabled.</p>
            <section className="sdk-acceptance" aria-labelledby="acceptance-title">
              <h2 id="acceptance-title">Extra check (optional)</h2>
              <label><input type="checkbox" checked={extraCheck} onChange={event => setExtraCheck(event.target.checked)}/>Add a check program</label>
              {!extraCheck && <p>The agent creates files from your instructions. Completion does not mean their correctness was independently verified.</p>}
              {extraCheck && <><p>This command runs on the host after the worker finishes. Choose a check you trust. Keep it outside the working folder if the worker must not edit it.</p>
              <div className="sdk-command-fields">
                <div><label htmlFor="task-program">Program</label>
                  <input id="task-program" className="input mono" required value={program} placeholder="node"
                    onChange={(event) => setProgram(event.target.value)} /></div>
                <div><label htmlFor="task-arguments">Arguments, one per line</label>
                  <textarea id="task-arguments" className="input mono" rows={3} value={argumentsText}
                    placeholder="/path/to/acceptance.mjs" onChange={(event) => { setArgumentsText(event.target.value); setLoadedArguments(null); }} /></div>
              </div></>}
            </section>
          </> : <>
            <label htmlFor="idea-domain">Who are these ideas for?</label>
            <textarea id="idea-domain" className="input sdk-goal" rows={4} required value={domain}
              placeholder="Offline tools for weekend cyclists" onChange={(event) => setDomain(event.target.value)} />
            {goalIssue && <p role="alert">{goalIssue}</p>}
            <p className="sdk-field-hint">Creates three ideas and a comparison brief in its own folder. Built-in checks verify the files and comparison. The ideas are hypotheses, not market research.</p>
          </>}
          <div className="sdk-start-options">
            <ProjectPicker value={project} onChange={setProject} onPending={setPendingProject} disabled={launch.isPending} />
            <label className="sdk-correction"><input type="checkbox" checked={correction}
              onChange={(event) => setCorrection(event.target.checked)} /> {mode === 'nodes' ? 'Allow one correction per step if its check fails' : 'Allow one correction if the check fails'}</label>
          </div>
          <WorkflowSignOff value={signOff} disabled={busy} onChange={setSignOff} />
          <section className="sdk-save" aria-label="Save workflow">
            <label htmlFor="workflow-name">Workflow name</label>
            <input ref={nameInput} id="workflow-name" className="input" maxLength={160} value={name} placeholder={namePlaceholder || (mode === 'nodes' ? 'Cycling app decision' : mode === 'ideator' ? 'Weekend app ideas' : 'Keyboard regression check')} onChange={(event) => { setName(event.target.value); save.reset(); }} />
            {namePlaceholder && !name.trim() && <p className="sdk-field-hint">Suggested name: “{namePlaceholder}”. <button type="button" className="btn btn--ghost" onClick={() => { setName(namePlaceholder); save.reset(); }}>Use this name</button></p>}
            {nameIssue && <p role="alert">{nameIssue}</p>}
            <p className="sdk-field-hint">Optional for a one-off task. Save these settings for next time.</p>
            {saved?.id && <div className="sdk-run-actions">
              <button className="btn btn--ghost" type="button" onClick={() => { setSaved(null); setConfirmRemove(false); save.reset(); remove.reset(); launch.reset(); }}>Use as a new workflow</button>
              <button className="btn btn--ghost" type="button" onClick={() => setConfirmRemove(true)}>Remove workflow</button>
            </div>}
            {confirmRemove && <div className="sdk-remove"><p>Remove “{saved?.name}” from saved workflows?</p><button className="btn" type="button" onClick={() => remove.mutate()}>Remove saved setup</button> <button className="btn btn--ghost" type="button" onClick={() => setConfirmRemove(false)}>Keep workflow</button></div>}
            {(save.error || remove.error) && <p ref={saveError} tabIndex={-1} role="alert">{(save.error || remove.error)?.message}</p>}
          </section>
          {launch.error && <p ref={launchError} tabIndex={-1} className="sdk-start-error" role="alert">{launch.error.message}</p>}
          {fillOpen && byReference && <WorkflowRunInputs inputs={declaredInputs} values={slotValues} disabled={busy} missing={serverMissing} onChange={setSlotValues} />}
          {needsSaveToRun && <p className="sdk-field-hint">This workflow asks for {declaredInputs.length} {declaredInputs.length === 1 ? 'input' : 'inputs'}. Save it first: a run with inputs uses the saved definition, so the values can change without the steps changing.</p>}
          <p className="sdk-field-hint">The run continues if you leave this page. You can inspect or cancel it from Runs.</p>
          <footer className="sdk-start-footer" role="region" aria-label="Workflow actions">
            <div className="sdk-action-status">
              {save.isSuccess && unchanged && <span role="status">Workflow saved</span>}
              {saved?.id && !unchanged && <span className="faint">Unsaved changes</span>}
              {/* title, not aria-label: the visible hint IS the accessible name. */}
              {reasons.map(reason => <button key={reason.hint} className="btn btn--ghost" type="button" title={reason.hint} onClick={reason.focus}>{reason.hint}</button>)}
              {fillOpen && byReference && missing.length > 0 && <span className="faint">Fill {missing.map(id => declaredInputs.find(input => input.id === id)?.label || id).join(', ')} to run</span>}
              {(save.error || remove.error) && <button className="btn btn--ghost" type="button" onClick={() => saveError.current?.focus()}>Review save error</button>}
              {launch.error && <button className="btn btn--ghost" type="button" onClick={() => launchError.current?.focus()}>Review start error</button>}
            </div>
            <div className="sdk-action-buttons">
              <button className="btn" type="button" disabled={!ready || !name.trim() || Boolean(nameIssue) || busy || unchanged || blocked} onClick={() => { if (ready && !busy && !blocked) save.mutate(); }}>{save.isPending ? 'Saving…' : saved?.id ? 'Save changes' : 'Save workflow'}</button>
              {/* Three DIFFERENT buttons, keyed apart, in one slot.
                  Unkeyed, React reconciled them as one element and only
                  rewrote its attributes — so the click that opened the input
                  panel turned that very element from type="button" into
                  type="submit" before the browser read the type, and the form
                  submitted. "Fill inputs and run" LAUNCHED the run, skipping
                  the panel it had just opened, whenever the values were
                  already filled in (which is exactly the "run again with the
                  same inputs" path). The key forces a remount; the
                  preventDefault says the intent out loud regardless. */}
              {needsSaveToRun
                ? <button key="save-to-run" className="btn btn--primary" type="button" disabled>Save to run with inputs</button>
                : byReference && !fillOpen
                ? <button key="fill-inputs" className="btn btn--primary" type="button" disabled={!ready || busy || blocked}
                    onClick={(event) => { event.preventDefault(); setFillOpen(true); }}>Fill inputs and run</button>
                : <button key="run" className="btn btn--primary" type="submit" disabled={!ready || busy || blocked || missing.length > 0}>
                    {launch.isPending ? 'Starting…' : mode === 'nodes' ? 'Run workflow' : mode === 'task' ? 'Start task' : 'Create app ideas'}
                  </button>}
            </div>
          </footer>
        </fieldset>
      </form>
    </div>
  );
}
