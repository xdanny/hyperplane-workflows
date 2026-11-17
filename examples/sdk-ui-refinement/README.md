# UI correction through the SDK

This runnable example replaces the retired `refine` graph loop with one SDK
conversation and an explicit browser acceptance command. No graph, simulator
driver or model voting panel is required.

From the repository root, after installing the pinned SDK and web dependencies:

```sh
make sdk-install
npm ci --prefix apps/web
cd apps/web && npx playwright install chromium
cd ../..
node examples/sdk-ui-refinement/run.mjs
node examples/sdk-ui-refinement/run.mjs --inject-failure
```

The example creates a fresh temporary project and prints its location. The model
writes only that project. A trusted checker outside the project opens its HTML
in Chromium, checks the starting count, button click and keyboard activation,
and saves a screenshot for each check. Browser network requests are blocked.

`--inject-failure` asks for an intentional increment-by-two defect on the first
turn. The fixed check rejects it; `runTask` returns the findings to the same SDK
thread for one correction. Checkpoints, SDK events, screenshots and the final
result live in the sibling evidence directory. A model transport failure or
failure to launch the acceptance command stops the example. Normal nonzero
command exits become correction feedback, including failures within that program;
use a custom verifier with `runChecked` if infrastructure failures need separate
classification. Ctrl-C cancels it; the run has a three-minute timeout and a
200,000-token between-turn budget. An in-flight turn may exceed that budget.

The checker is application code: replace it with the build, rendering and visual
checks your application actually needs. These assertions prove counter behavior
and keyboard activation, not design quality or comprehensive accessibility.
The controlled trial is not a natural model-reliability measurement.

This standalone example does not create database-owned delivery records or
provide crash recovery. The application backlog route uses the same `runTask`
implementation with the database checkpoint lifecycle.
