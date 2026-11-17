// Project one SDK turn into the small result used by both the application
// bridge and checked workflows. The SDK owns the stream and conversation.
export async function runTurn(thread, input, { signal, outputSchema, onEvent = async () => {} } = {}) {
  signal?.throwIfAborted();
  let text = '';
  let usage;
  let failure;
  const { events } = await thread.runStreamed(input, { signal, outputSchema });
  for await (const event of events) {
    signal?.throwIfAborted();
    await onEvent(event);
    if (event.type === 'item.completed' && event.item.type === 'agent_message') text = event.item.text;
    if (event.type === 'turn.failed') failure = event.error.message;
    if (event.type === 'error') failure = event.message;
    if (event.type === 'turn.completed') {
      if (usage) throw new Error('SDK stream completed more than once');
      usage = event.usage || {};
    }
  }
  signal?.throwIfAborted();
  if (failure || !usage) throw new Error(failure || 'SDK stream ended before turn completion');
  return { threadId: thread.id, text, usage };
}
