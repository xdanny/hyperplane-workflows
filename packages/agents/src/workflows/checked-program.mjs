import path from 'node:path';
import { inventoryFiles, hashFile } from './files.mjs';

// Observe one caller-named program executed by SDK tools. This never executes
// model-generated code on the host and does not decide domain correctness.
export function checkedProgram(workspace, filename = 'checks.sh', { signal } = {}) {
  if (!/^[a-z][a-z0-9-]*\.sh$/.test(filename)) throw new Error('Use a fixed shell program filename');
  const command = `sh ${filename}`;
  const quoted = filename.replaceAll('.', '\\.');
  const matching = new RegExp(`^(?:/bin/(?:bash|zsh|sh) -(?:lc|c) )?(?:sh ${quoted}|'sh ${quoted}'|"sh ${quoted}")$`);
  const inventory = () => inventoryFiles(path.dirname(workspace), path.basename(workspace), { signal });
  const digest = files => {
    const program = files.find(file => file.path === path.basename(workspace) + '/' + filename);
    if (!program) throw new Error(`Missing check program: ${filename}`);
    return program.sha256;
  };
  let started, execution;
  return {
    description: `Create ${filename} and run exactly ${command} as a separate SDK command, with no chaining or directory changes. The host requires an observed successful execution and unchanged output inventory. Generated programs run only inside this SDK sandbox.`,
    async onEvent(event) {
      const item = event.item;
      if (item?.type !== 'command_execution' || !matching.test(item.command)) return;
      if (event.type === 'item.started') {
        execution = undefined;
        // Check containment before reading a model-owned file from the host.
        started = {id:item.id,hash:await hashFile(workspace, filename, {signal})};
      }
      if (event.type === 'item.completed' && started?.id === item.id && item.exit_code === 0 && item.status === 'completed') {
        const files = await inventory();
        if (started.hash !== digest(files)) throw new Error(`${filename} changed during execution`);
        execution = files;
      }
    },
    async verify() {
      if (!execution) throw new Error(`Run exactly ${command} through the SDK and complete it successfully before handing off`);
      if (JSON.stringify(execution) !== JSON.stringify(await inventory())) throw new Error(`Outputs changed after ${filename} finished; rerun it`);
    },
  };
}
