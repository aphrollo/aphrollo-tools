level: minor

The laws are now planned in one place. A plan names which laws judge which files at the edit, commit and merge stages, loads the laws once per process, and the edit and commit gates both read their law list from it.

### What you will notice

- A commit refusal by a deny law at a file you edited is now named at the edit, with the one exception of the module-graph laws on a write the agent harness did not hand over content for (the commit judges those).
- Repeated law loads inside one hook cost one read of the law files, not one per judge.
