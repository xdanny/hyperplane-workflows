import { useState } from 'react';
import type { DragEvent } from 'react';
import type { ContextSlot, SDKWorkflowNode, WorkflowCheck } from '../api';
import './WorkflowNodes.css';
import { nodeTextIssue } from '../workflowValidation';
import type { WorkflowIssue } from '../workflowValidation';
import { WorkflowProfileInputs } from './WorkflowProfileInputs';
import { WorkflowCheckRequirements } from './WorkflowCheckRequirements';
import { WorkflowStepAccess } from './WorkflowStepAccess';
import { WorkflowGraph } from './WorkflowGraph';
import { WorkflowInputInsert, WorkflowUsesInputs } from './WorkflowInputTokens';
import { dependentSteps, moveStep, nodeDependencies, wouldCycle } from '../workflowGraph';
import { nodeKind } from '../workflowNodePresentation';

const templates = [
  { title: 'Research', kind: 'research' as const, instruction: 'Research the goal with live web search. Cite sources and explain evidence limits.' },
  { title: 'Explore', instruction: 'Explore the goal. List useful approaches, assumptions and questions that need evidence.' },
  { title: 'Compare', instruction: 'Compare the approaches from the previous steps. Explain the tradeoffs and recommend one.' },
  { title: 'Draft', instruction: 'Use the previous steps to write a clear, practical first draft.' },
  { title: 'Create files', kind: 'task' as const, instruction: 'Create the requested files in this step’s managed folder. Explain what you produced and any limitations.' },
  { title: 'Approval', kind: 'approval' as const, instruction: 'Review the earlier documents and approve them before the next step starts.' },
  { title: 'Review', instruction: 'Review the previous documents against the goal. Identify gaps, unsupported claims and specific improvements.' },
  { title: 'Independent review', kind: 'check' as const, instruction: 'Independently check the earlier documents against the goal. Fail unsupported conclusions or missing requirements; explain the findings.' },
];
const transferType = 'application/x-hyperplane-node';
const makeNode = (title: string, instruction: string, kind: SDKWorkflowNode['kind'] = 'document'): SDKWorkflowNode => ({ id: `step-${crypto.randomUUID()}`, title, instruction, kind });

export function WorkflowNodes({ nodes, onChange, disabled, checks, issue, instruction, inputs = [] }: { instruction?: string; issue?: WorkflowIssue; nodes: SDKWorkflowNode[]; onChange: (nodes: SDKWorkflowNode[]) => void; disabled: boolean; checks?: WorkflowCheck[]; inputs?: ContextSlot[] }) {
  const [description, setDescription] = useState('');
  const [message, setMessage] = useState('');
  const [over, setOver] = useState<number | null>(null);
  const add = (title: string, instruction: string, index = nodes.length, kind: SDKWorkflowNode['kind'] = 'document') => {
    if (disabled || nodes.length >= 20) return;
    const next = [...nodes]; next.splice(index, 0, makeNode(title, instruction, kind)); onChange(next);
    setMessage(`${title} added at step ${index + 1}.`);
  };
  const move = (id: string, index: number) => {
    if (disabled) return;
    const moving = nodes.find(n => n.id === id);
    if (!moving) return;
    const next = moveStep(nodes, id, index);
    const to = next.findIndex(n => n.id === id);
    // Moving a step rewires the explicit links the move would otherwise break.
    // Say so: a silent rewire is as surprising as a silently broken gate.
    const rewired = next.filter(node => JSON.stringify(node.dependsOn) !== JSON.stringify(nodes.find(before => before.id === node.id)?.dependsOn)).length;
    onChange(next);
    setMessage(`${moving.title} moved to step ${to + 1}.${rewired ? ` ${rewired} ${rewired === 1 ? 'connection was' : 'connections were'} rewired so the order still gates.` : ''}`);
  };
  const drop = (event: DragEvent, index: number) => {
    event.preventDefault(); setOver(null);
    if (disabled) return;
    try {
      const item = JSON.parse(event.dataTransfer.getData(transferType));
      if (typeof item.id === 'string') {
        const from = nodes.findIndex(n => n.id === item.id);
        move(item.id, from < index ? index - 1 : index);
      } else if (Number.isInteger(item.template) && templates[item.template]) {
        const t = templates[item.template]; add(t.title, t.instruction, index, t.kind);
      }
    } catch { /* External drags are not workflow nodes. */ }
  };
  const lines = description.split(/\r?\n/).map(s => s.trim().replace(/^(?:[-*] |\d+[.)] )/, '')).filter(Boolean);
  const addDescriptions = () => {
    if (disabled || !lines.length || nodes.length + lines.length > 20) return;
    onChange([...nodes, ...lines.map(line => makeNode(line.slice(0, 80), line))]);
    setDescription(''); setMessage(`${lines.length} steps added. Review and edit them below.`);
  };
  const zone = (index: number) => <div className={`workflow-drop ${over === index ? 'is-over' : ''}`} data-testid={`drop-${index}`}
    onDragOver={event => { if (!disabled && event.dataTransfer.types.includes(transferType)) { event.preventDefault(); setOver(index); } }}
    onDragLeave={() => setOver(null)} onDrop={event => drop(event, index)}><span>{over === index ? 'Drop step here' : '↓'}</span></div>;

  return <section className="workflow-builder" aria-label="Workflow steps">
    <div className="workflow-builder-intro"><h2>Build your steps</h2><p>Drag a step type into the list, or click to add it. Steps follow the previous step by default. Choose dependencies to branch into parallel work or bring results together.</p></div>
    <div className="workflow-palette" aria-label="Step library">{templates.map((item, index) => <button key={item.title} type="button" className="workflow-tool" disabled={disabled || nodes.length >= 20}
      draggable={!disabled && nodes.length < 20} onDragStart={event => { event.dataTransfer.setData(transferType, JSON.stringify({ template: index })); event.dataTransfer.effectAllowed = 'copy'; }}
      onClick={() => add(item.title, item.instruction, nodes.length, item.kind)}><strong>{item.title}</strong><span>{item.instruction}</span></button>)}</div>
    <details className="workflow-describe">
      <summary>Describe your steps</summary>
      <label htmlFor="node-descriptions">What should each step do? One step per line.</label>
      <textarea id="node-descriptions" className="input" rows={4} value={description} disabled={disabled}
        placeholder={'List three ideas for a weekend cycling app\nCompare them for ease of use\nWrite a one-page plan for the best idea'} onChange={event => setDescription(event.target.value)} />
      <p>Each line starts as a document step, using your words as its instructions. Choose its type below: keep a document, add research or review, wait for approval, or create files. Extra checks are optional.</p>
      <button type="button" className="btn" disabled={disabled || !lines.length || nodes.length + lines.length > 20} onClick={addDescriptions}>Add described steps</button>
      {nodes.length + lines.length > 20 && <p role="alert">A workflow can have up to 20 steps. Shorten the list before adding it.</p>}
    </details>
    <WorkflowGraph nodes={nodes} instruction={instruction} checks={checks} onSelect={id => document.getElementById(`title-${id}`)?.focus()} />
    <div className="workflow-sequence" aria-label="Step sequence">
      <div className="workflow-boundary">Start with your goal</div>
      {nodes.length === 0 && <p className="workflow-empty">Drop a step below to begin, or describe the steps above.</p>}
      {zone(0)}
      {nodes.map((node, index) => <div key={node.id}>
        <article className="workflow-node" aria-label={`Step ${index + 1}: ${node.title}`}>
          <div className="workflow-node-heading"><span className="workflow-number">{index + 1}</span>
            <span className="workflow-handle" draggable={!disabled} title="Drag to move step" aria-label={`Drag ${node.title}`}
              onDragStart={event => { event.dataTransfer.setData(transferType, JSON.stringify({ id: node.id })); event.dataTransfer.effectAllowed = 'move'; }}>⠿</span>
            <label htmlFor={`title-${node.id}`} className="sr-only">Step {index + 1} name</label>
            <input id={`title-${node.id}`} className="input" required maxLength={160} value={node.title} disabled={disabled}
              onChange={event => onChange(nodes.map(n => n.id === node.id ? { ...n, title: event.target.value } : n))} />
          </div>
          <label htmlFor={`kind-${node.id}`}>Step {index + 1} type</label>
          <select id={`kind-${node.id}`} className="input" value={node.kind ?? 'document'} disabled={disabled}
            onChange={event => onChange(nodes.map(n => {
              if (n.id !== node.id) return n;
              const { check, acceptance, inputs, access, toolchain, sideEffect, ...rest } = n;
              return { ...rest, kind: event.target.value as SDKWorkflowNode['kind'], ...(event.target.value === 'task' ? acceptance ? { acceptance, ...(inputs ? { inputs } : {}) } : (check?.length ? { check } : {}) : {}) };
            }))}>
            <option value="document">Create a document</option><option value="research">Research with live sources</option><option value="check">Independent model review</option><option value="approval">Wait for my approval</option>
            <option value="task">Create files</option>
          </select>
          <label htmlFor={`dependencies-${node.id}`}>Step {index + 1} starts after</label>
          <select id={`dependencies-${node.id}`} className="input" disabled={disabled} value={node.dependsOn === undefined ? 'previous' : 'custom'}
            onChange={event => onChange(nodes.map(n => {
              if (n.id !== node.id) return n;
              if (event.target.value === 'custom') return { ...n, dependsOn: nodeDependencies(nodes, index) };
              const { dependsOn, ...rest } = n; return rest;
            }))}>
            <option value="previous">{index === 0 ? 'Your goal (first in the list)' : `Previous step: ${nodes[index - 1].title}`}</option>
            <option value="custom">Choose dependencies</option>
          </select>
          {node.dependsOn !== undefined && <fieldset className="workflow-dependencies" disabled={disabled}>
            <legend>Dependencies for {node.title || `step ${index + 1}`}</legend>
            <p>Select every step whose results this step needs. No selection means it can start with your goal. Reviews and approvals need earlier evidence; the validator checks these requirements. Steps that would then wait on this one are not listed — that would be a loop.</p>
            {nodes.filter(other => other.id !== node.id && (node.dependsOn!.includes(other.id) || !wouldCycle(nodes, node.id, other.id))).map(other => <label key={other.id}>
              <input type="checkbox" checked={node.dependsOn!.includes(other.id)} onChange={event => onChange(nodes.map(n => n.id !== node.id ? n : { ...n, dependsOn: event.target.checked ? [...n.dependsOn!, other.id] : n.dependsOn!.filter(id => id !== other.id) }))} />
              <span>{other.title || 'Untitled step'}</span>
            </label>)}
            {node.dependsOn.filter(id => !nodes.some(other => other.id === id)).map(id => <label key={id} className="workflow-missing-dependency">
              <input type="checkbox" checked onChange={() => onChange(nodes.map(n => n.id !== node.id ? n : { ...n, dependsOn: n.dependsOn!.filter(parent => parent !== id) }))} />
              <span>Missing step: {id}. Uncheck to remove this connection.</span>
            </label>)}
          </fieldset>}
          {node.kind === 'research' && <p className="sdk-field-hint">Uses live web search. Searches and citations are recorded; their presence alone does not prove accuracy.</p>}
          {node.kind === 'check' && <p className="sdk-field-hint">A separate model conversation reviews earlier documents against these instructions. It can check sources online when earlier steps include research. A failed review stops the workflow.</p>}
          {(nodeTextIssue(node) || issue?.nodeId === node.id) && <p role="alert">{nodeTextIssue(node) ?? issue?.message}</p>}
          {node.kind === 'task' && <div className="sdk-start-form">
            <label htmlFor={`acceptance-${node.id}`}>Step {index + 1} extra check</label>
            <select id={`acceptance-${node.id}`} className="input" value={node.acceptance ?? (node.check?.length ? 'custom' : 'none')} disabled={disabled} onChange={event => onChange(nodes.map(n => {
              if (n.id !== node.id) return n;
              const { check, acceptance, inputs, ...rest } = n;
              if (event.target.value === 'none') return rest;
              if (event.target.value === 'custom') return { ...rest, check: [''] };
              const selected = checks?.find(check => check.id === event.target.value);
              if (!selected) return n;
              const keys = [...(selected.inputs?.required ?? []), ...(selected.inputs?.optional ?? [])];
              return { ...rest, acceptance: selected.id, ...(keys.length ? { inputs: Object.fromEntries(keys.map(key => [key, ''])) } : {}) };
            }))}>
              <option value="none">No extra check</option>
              <option value="custom">A program I supply</option>
              {node.acceptance && !checks?.some(check => check.id === node.acceptance) && <option value={node.acceptance}>{node.acceptance} (requirements unavailable)</option>}
              {checks?.map(check => <option key={check.id} value={check.id}>{check.title}</option>)}
            </select>
            <WorkflowCheckRequirements acceptance={node.acceptance} checks={checks}/>
            <WorkflowProfileInputs node={node} index={index} check={checks?.find(check => check.id === node.acceptance)} disabled={disabled} onChange={updated=>onChange(nodes.map(n=>n.id===node.id?updated:n))}/>
            {!node.acceptance && !node.check?.length && <p>The agent creates files from your instructions. Completion means files were produced; their correctness has not been independently verified. Add a check when you need one.</p>}
            {!node.acceptance && Boolean(node.check?.length) && <>
            <p>This step can write files in its own managed folder. Your check runs there after the SDK finishes. Use a trusted check; it runs on the host. Keep acceptance rules outside this folder when they must remain unchanged.</p>
            <label htmlFor={`program-${node.id}`}>Step {index + 1} acceptance program</label>
            <input id={`program-${node.id}`} className="input" required value={node.check?.[0] ?? ''} disabled={disabled} placeholder="python3"
              onChange={event => onChange(nodes.map(n => n.id === node.id ? { ...n, check: [event.target.value, ...(n.check?.slice(1) ?? [])] } : n))} />
            <label htmlFor={`args-${node.id}`}>Step {index + 1} check arguments, one per line</label>
            <textarea id={`args-${node.id}`} className="input" rows={3} disabled={disabled} value={node.check?.slice(1).join('\n') ?? ''} placeholder="/absolute/path/to/acceptance.py"
              onChange={event => onChange(nodes.map(n => n.id === node.id ? { ...n, check: [n.check?.[0] ?? '', ...(event.target.value === '' ? [] : event.target.value.split('\n'))] } : n))} />
            <p className="sdk-field-hint">Arguments are literal values; shell quoting is unnecessary. Earlier outputs are read-only inputs. Passing means your check passed, not that every result is correct.</p>
            </>}
            <WorkflowStepAccess node={node} index={index} disabled={disabled} inputs={inputs} onChange={updated => onChange(nodes.map(n => n.id === node.id ? updated : n))}/>
          </div>}
          {node.kind === 'approval' && dependentSteps(nodes, node.id).length === 0 && <p className="workflow-approval-warning" role="alert">No step waits for this approval — the workflow will finish without your decision.</p>}
          <div className="workflow-instruction-heading">
            <label htmlFor={`description-${node.id}`}>Step {index + 1} instructions</label>
            <WorkflowInputInsert inputs={inputs} target={`description-${node.id}`} describes={`step ${index + 1} instructions`} disabled={disabled}
              onInsert={next => onChange(nodes.map(n => n.id === node.id ? { ...n, instruction: next } : n))} />
          </div>
          <textarea id={`description-${node.id}`} className="input" required rows={3} maxLength={16000} value={node.instruction} disabled={disabled}
            onChange={event => onChange(nodes.map(n => n.id === node.id ? { ...n, instruction: event.target.value } : n))} />
          <div className="workflow-node-footer"><small>{nodeDependencies(nodes, index).length ? `Waits for ${nodeDependencies(nodes, index).length} ${nodeDependencies(nodes, index).length === 1 ? 'dependency' : 'dependencies'}` : 'Starts with your goal'} · {node.kind === 'task' && (node.acceptance || node.check?.length) ? 'Files and work summary with an extra check' : nodeKind(node).output}<WorkflowUsesInputs inputs={inputs} text={node.instruction} /></small>
            <div><button type="button" className="btn btn--ghost" aria-label={`Move step ${index + 1} up`} disabled={disabled || index === 0} onClick={() => move(node.id, index - 1)}>↑</button>
              <button type="button" className="btn btn--ghost" aria-label={`Move step ${index + 1} down`} disabled={disabled || index === nodes.length - 1} onClick={() => move(node.id, index + 1)}>↓</button>
              <button type="button" className="btn btn--ghost" aria-label={`Remove step ${index + 1}`} disabled={disabled} onClick={() => { const remaining = nodes.filter(n => n.id !== node.id).map(n => n.dependsOn?.includes(node.id) ? { ...n, dependsOn: n.dependsOn.filter(id => id !== node.id) } : n); const unlinked = nodes.filter(n => n.id !== node.id && n.dependsOn?.includes(node.id)).length; onChange(remaining); setMessage(unlinked ? `${node.title} removed. ${unlinked} ${unlinked === 1 ? 'step' : 'steps'} that waited for it no longer ${unlinked === 1 ? 'does' : 'do'}; check their dependencies.` : `${node.title} removed.`); }}>Remove</button></div>
          </div>
        </article>{zone(index + 1)}
      </div>)}
      <div className="workflow-boundary">Review your results</div>
    </div>
    <p className="workflow-feedback" role="status" aria-live="polite">{message}</p>
    <p className="sdk-field-hint">Document steps produce Markdown. File creation uses your instructions. Extra checks are optional and have their own requirements. Research steps use live sources. Independent reviews can stop the workflow on a failed criterion; approvals wait for your decision. External app actions are unavailable.</p>
  </section>;
}
