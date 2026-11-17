import { useState } from 'react';
import type { ContextSlot, SDKWorkflowNode } from '../api';
import { WorkflowInputInsert } from './WorkflowInputTokens';

type Access = NonNullable<SDKWorkflowNode['access']>;
const list = (text: string) => text.split('\n').map(s => s.trim()).filter(Boolean);

/**
 * Two different permissions, asked separately.
 *
 * Reading a local file used to require inventing a network host: "Readable
 * inputs" appeared only under one "Allow connections" switch, and with no host
 * the workflow could not be saved at all — so a step that needed to read
 * /data/sales.csv was granted `localhost`, access nobody wanted and the receipt
 * then recorded. Local reads and network hosts are now independent grants; a
 * step can read a file with no network at all.
 */
export function WorkflowStepAccess({node,index,disabled,inputs = [],onChange}: {node: SDKWorkflowNode; index: number; disabled: boolean; inputs?: ContextSlot[]; onChange: (node: SDKWorkflowNode) => void}) {
  const [reads, setReads] = useState(Boolean(node.access?.readPaths?.length));
  const [network, setNetwork] = useState(Boolean(node.access?.hosts.length));
  const access = node.access;
  const update = (key: keyof Access, text: string) => onChange({...node,access:{...node.access!,[key]:list(text)}});
  const enable = (next: {reads?: boolean; network?: boolean}) => {
    const wantReads = next.reads ?? reads, wantNetwork = next.network ?? network;
    setReads(wantReads); setNetwork(wantNetwork);
    if (wantReads || wantNetwork) {
      onChange({...node, access: {
        hosts: wantNetwork ? node.access?.hosts ?? [] : [],
        ...(wantReads ? {readPaths: node.access?.readPaths ?? []} : {}),
        ...(node.access?.environment?.length ? {environment: node.access.environment} : {}),
      }, ...(wantNetwork ? {} : {toolchain: false, sideEffect: false})});
      return;
    }
    const {access: _removed, toolchain: _toolchain, sideEffect: _sideEffect, ...rest} = node;
    onChange(rest);
  };
  const summary = [reads ? `${access?.readPaths?.length ?? 0} readable ${(access?.readPaths?.length ?? 0) === 1 ? 'path' : 'paths'}` : '',
    network ? `${access?.hosts.length ?? 0} allowed ${(access?.hosts.length ?? 0) === 1 ? 'host' : 'hosts'}` : ''].filter(Boolean).join(' · ');
  return <details className="workflow-step-access">
    <summary>Files and connections · {summary || 'Off'}</summary>
    <p>Enable only what this step needs. Descriptions do not grant access. These settings apply when its SDK commands run.</p>
    <label><input type="checkbox" disabled={disabled} checked={reads} onChange={e => enable({reads: e.target.checked})}/>Read local files or folders in step {index + 1}</label>
    {reads && <div className="sdk-start-form">
      <div className="workflow-instruction-heading">
        <label htmlFor={`reads-${node.id}`}>Step {index + 1} readable inputs, one path per line</label>
        <WorkflowInputInsert inputs={inputs} target={`reads-${node.id}`} describes={`step ${index + 1} readable inputs`} disabled={disabled}
          onInsert={next => update('readPaths', next)}/>
      </div>
      <textarea id={`reads-${node.id}`} className="input" rows={2} disabled={disabled} value={access?.readPaths?.join('\n') ?? ''} placeholder="/data/sales.csv" onChange={e => update('readPaths',e.target.value)}/>
      <p className="sdk-field-hint">Absolute paths to local source files or folders. A folder grants read access to everything inside it. System tools and earlier workflow outputs stay readable without listing them. This needs no network host. A path may use an input token, so the file can change each run.</p>
    </div>}
    <label><input type="checkbox" disabled={disabled} checked={network} onChange={e => enable({network: e.target.checked})}/>Allow network hosts for step {index + 1}</label>
    {network && <div className="sdk-start-form">
      <label htmlFor={`hosts-${node.id}`}>Step {index + 1} allowed hosts, one per line</label>
      <textarea id={`hosts-${node.id}`} className="input" rows={3} disabled={disabled} value={access?.hosts.join('\n') ?? ''} placeholder={'data.example.com\npypi.org\nfiles.pythonhosted.org'} onChange={e => update('hosts',e.target.value)}/>
      <p className="sdk-field-hint">Use exact host names. Redirects and package downloads may need additional hosts. This allows connections to a host; it does not restrict them to read requests. Use a read-only account for source data.</p>
      {!access?.hosts.length && <p role="alert">Name at least one host, or turn network access off for this step.</p>}
    </div>}
    {(reads || network) && <div className="sdk-start-form">
      <label htmlFor={`connections-${node.id}`}>Step {index + 1} connection names, one per line</label>
      <textarea id={`connections-${node.id}`} className="input" rows={2} disabled={disabled} value={access?.environment?.join('\n') ?? ''} placeholder="WORKFLOW_SALES_CONNECTION" onChange={e => update('environment',e.target.value)}/>
      <p className="sdk-field-hint">Enter environment variable names already configured on the run host. Never paste passwords or connection values. A missing connection stops the step before the SDK starts.</p>
    </div>}
    {network && <>
      <label><input type="checkbox" disabled={disabled} checked={!!node.toolchain} onChange={e => onChange({...node,toolchain:e.target.checked,sideEffect:e.target.checked})}/>Allow step {index + 1} to install temporary tools</label>
      <p className="sdk-field-hint">Requires uv on the run host and the hosts the packages come from. This grants installation inside a temporary environment, which is removed when the step ends. The temporary environment is excluded from delivered files.</p>
    </>}
  </details>;
}
