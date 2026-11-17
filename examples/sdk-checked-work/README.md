# Checked files in an SDK workflow

This example reads four synthetic rows, creates a CSV containing category totals,
runs a fixed acceptance program, and passes the checked file to a document step.
It uses the same SDK node extension as the visual and description editor.

After `make sdk-install`, run from the repository root:

```sh
node examples/sdk-checked-work/workflow.mjs /absolute/path/to/a/new-output-folder
```

The folder must not exist. This makes actual model calls using the configured
Codex login. The model can write its `build/` folder; the caller's input, reference
data and acceptance program remain outside that writable folder. The check runs
on the host, so supply trusted programs. The example allows one correction when
the check returns a normal failing exit code, with a ten-minute timeout and a
300,000-token between-turn budget.

The expected output is `build/totals.csv`: food 15.00 and travel 6.00. The host
records hashes in `build.md`; `explain.md` summarizes the result. `receipt.json`
records execution and findings. These checks prove this fixture's totals and
unchanged input, not general data-pipeline correctness.

In the application, drag **Checked work** into the sequence, or change a described
step to **Create files and run my check**. Set its acceptance program and literal
arguments. Save or run the same definition; an optional Approval step presents
both the summary and actual checked files. Files are rechecked before later work.
Installing dependencies, editing an existing project and external app operations
are outside this managed-folder step. Use the separate Project task flow for an
existing project. Larger data-recipe migration remains in progress.
