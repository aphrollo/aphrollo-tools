level: patch

`workspace merge` merges directly in a repository whose plan has no rulesets, instead of refusing.

### What you will notice

- A private repository of a GitHub Free organisation answers the branch-rules read with a 403 saying "Upgrade to GitHub Pro or make this repository public to enable this feature." That now means no merge queue, as a 404 does. A 403 that is about the token (scope, SSO) still refuses with the `gh auth status` hint.
