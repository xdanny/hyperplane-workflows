# Reviewer instructions

These five files are editable review perspectives used by the same general agent adapter. They define criteria and wording, not separate agent runtimes or execution permissions.

| Registry ID | Focus |
|---|---|
| [priya-nonengineer](priya-nonengineer.md) | Understandable actions and honest expectations |
| [marcus-data-engineer](marcus-data-engineer.md) | Data semantics and operational evidence |
| [sofia-analyst](sofia-analyst.md) | Defensible numbers and decisions |
| [dev-ml-engineer](dev-ml-engineer.md) | Evaluation evidence and model claim limits |
| [quinn-tech-editor](quinn-tech-editor.md) | Clear explanations that preserve uncertainty |

`make seed` imports the charters into the reviewer registry. Filename stems are stable IDs; content hashes identify the version used for a review. Edit the file and seed again to refresh it. Preserve the `IDENTITY`, `RULES`, `COMPETENCE`, `AUDIT PROTOCOL`, and `SKILL-REVIEW PROTOCOL` headings because the prompt builder reads those sections.

The current review targets are a run’s recorded SDK evidence and a skill’s instruction and support evidence. Run reviews receive checkpoint phases, optional-check verification states, findings, file names, and bounded session-log tails. They do not establish browser behavior, deployment success, or correctness beyond the supplied checks.

```sh
hyperplane persona list
hyperplane persona list --competent-for skill:ai-engineering
hyperplane persona review dev-ml-engineer --target skill:ai-engineering
hyperplane persona review quinn-tech-editor --target run:RUN_ID
```

The corresponding APIs are `GET /api/personas`, `POST /api/personas/{id}/review`, and `GET /api/persona-reviews`. A person selects the reviewer. Skill routing uses explicit competence declarations; free-text focus descriptions help selection without implying automatic expertise.

Reviews are advisory records. They neither grant permissions nor approve skills or workflow steps. For a separately authorized browser review, use a charter as criteria and report only interactions actually observed.

Keep current instructions in these files. Store review results through the review API or with the evidence for the task, rather than appending audit histories to the charters. The pre-cleanup originals are archived outside the checkout in `../hyperplane-cleanup-archive-20260906/persona-charters-20260907/`, with verified file hashes in its manifest.
