// Dependency planning belongs to the SDK workflow extension. Omitted edges keep
// the original ordered workflow; explicit [] deliberately starts a new root.
export function dependencyGraph(nodes) {
  const positions = new Map(nodes.map((node, index) => [node.id, index]));
  const explicit = nodes.some(node => node.dependsOn != null);
  const dependencies = nodes.map((node, index) => {
    const ids = node.dependsOn == null ? (index ? [nodes[index - 1].id] : []) : node.dependsOn;
    const fail = message => { throw Object.assign(new Error(message), { nodeId: node.id }); };
    if (!Array.isArray(ids) || ids.length > 20 || ids.some(id => typeof id !== 'string') || new Set(ids).size !== ids.length) fail('Dependencies must be a list of distinct step IDs');
    return ids.map(id => {
      if (!positions.has(id)) fail(`Unknown dependency ${id}`);
      if (id === node.id) fail('A step cannot depend on itself');
      return positions.get(id);
    });
  });
  const ancestors = nodes.map(() => new Set()), order = [], active = new Set(), done = new Set();
  function visit(index) {
    if (done.has(index)) return;
    if (active.has(index)) throw Object.assign(new Error('Workflow dependencies contain a cycle'), { nodeId: nodes[index].id });
    active.add(index);
    for (const parent of dependencies[index]) {
      visit(parent);
      ancestors[index].add(parent);
      for (const ancestor of ancestors[parent]) ancestors[index].add(ancestor);
    }
    active.delete(index);
    done.add(index);
    order.push(index);
  }
  nodes.forEach((_, index) => visit(index));
  return { explicit, dependencies, ancestors, order,
    previous: index => order.filter(parent => ancestors[index].has(parent)),
    precedes: (a, b) => ancestors[positions.get(b.id)].has(positions.get(a.id)),
  };
}
