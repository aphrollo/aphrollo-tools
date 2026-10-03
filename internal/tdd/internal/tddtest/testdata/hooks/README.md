# Recorded hook payloads

The JSON files in this directory are what the agent harness really sends a
hook on stdin, one file per payload, one or two per event type.

## Recorded where, when, how

- Recorded on 2026-10-02 on Windows 11, from one headless (`-p`) agent-harness
  session in a throwaway git repo, bypass-permissions mode, effort `medium`.
- A hook for each event in the table below was wired to one script that wrote its
  stdin, unchanged, to a file named for the event and a nanosecond timestamp:
  `cat > "payloads/${event}-$(date +%s%N).json"`. The script exited 0 and
  printed nothing, so no hook altered the run.
- A scripted prompt drove one session through a file write, a shell command, a
  failing shell command, a subagent started with worktree isolation (which
  writes a file in its own worktree), and `EnterWorktree` / `ExitWorktree`.
  34 payloads came out of it; the ones kept here are the ones that show a field
  a gate decision can rest on.
- The payloads carry no harness version field, so the version is not recorded
  here.

## What was scrubbed

Only values, never field names, key order or shape:

- the user's home directory (`C:\Users\<user>`) became `C:\Users\dev`;
- the session's working directory became `C:\Users\dev\spike-hooks`, so a
  subagent's worktree reads `C:\Users\dev\spike-hooks\.claude\worktrees\agent-a26c10f2ce9a15725`;
- the transcript directory slug became `C--Users-dev-spike-hooks`;
- the session id became `00000000-0000-4000-8000-000000000001` and the prompt
  id `00000000-0000-4000-8000-000000000002`.

Tool-use ids and the subagent's agent id are random handles, not identities,
and stay as recorded.

## Files

| File | Event | What it shows |
| --- | --- | --- |
| `sessionstart.json` | SessionStart | `source`; no `permission_mode`, no `prompt_id` |
| `userpromptsubmit.json` | UserPromptSubmit | `prompt` |
| `pretooluse_write.json` | PreToolUse | an edit: `tool_input.file_path`, `content` |
| `pretooluse_bash.json` | PreToolUse | a shell call: `tool_input.command` |
| `pretooluse_subagent_write.json` | PreToolUse | `agent_id`, `agent_type`, `cwd` = the subagent's worktree |
| `posttooluse_write.json` | PostToolUse | `tool_response` is an object with `type`, `filePath`, `structuredPatch`; it has no `success` |
| `posttooluse_subagent_write.json` | PostToolUse | the same, from a subagent |
| `posttoolusefailure_bash.json` | PostToolUseFailure | `error`, `is_interrupt`; no `tool_response` |
| `posttoolbatch.json` | PostToolBatch | `tool_calls[]`, each with `tool_name`, `tool_input`, `tool_use_id`, `tool_response` |
| `posttoolbatch_subagent.json` | PostToolBatch | the same, from a subagent |
| `subagentstart.json` | SubagentStart | `agent_id`, `agent_type`, `cwd` = the subagent's worktree |
| `subagentstop.json` | SubagentStop | `cwd` = the subagent's worktree, `stop_hook_active`, `agent_transcript_path` |
| `stop.json` | Stop | `stop_hook_active`, `last_assistant_message`; no `agent_id` |
| `sessionend.json` | SessionEnd | `reason`; no `permission_mode` |

`handwritten/taskcompleted.json` is the one file here that was not recorded:
the session never raised a TaskCompleted event (the task tool was not available
to it), so that payload is written from the harness's documented fields and
stays so until a recording replaces it.
