# Local model execution

Local inference is a separate choice from where the agent runs. The application
and SDK worker execute on the backend host. Choosing a local provider changes
where inference happens; it does not move workflow code into the browser.

Skill evaluation, judging and optimization retain local-provider adapters.
`LOCAL_LLM_BASE_URL` and `LOCAL_LLM_MODEL` configure that inference endpoint.
These supporting tools are separate from saved workflow execution: the ordered
SDK workflow editor has no per-step provider selector. Graph engine selectors
and graph-node optimization have been removed.

The local adapter uses isolated provider configuration without inheriting OpenAI
account credentials. Context files and output schemas are checked before a model
request; returned JSON must satisfy the requested schema. Availability alone does
not prove model quality or tool compatibility.

An SDK agent provider must support the endpoint protocol used by the installed
SDK. A server offering Chat Completions alone is not evidence of compatibility
with SDK tool execution. Test an actual request and a bounded tool operation
before relying on a provider for agent work.

Hardware choice, provider installation and model quality are outside this runtime
contract. Old host-specific benchmark results are not a compatibility guarantee
for another model or server version. See [runtime.md](runtime.md) for shared
execution and verification rules.
