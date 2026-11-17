/** Display names only; callers retain the original path for access and inspection. */
export function workflowFileLabel(path: string, nodes?: readonly { id: string; title: string }[]): string {
  const relative = path.startsWith('delivery/') ? path.slice('delivery/'.length) : path;
  for (const node of nodes ?? []) {
    if (relative === `${node.id}.md`) return node.title;
    if (relative.startsWith(`${node.id}/`)) return `${node.title} · ${relative.slice(node.id.length + 1)}`;
  }
  return path;
}
