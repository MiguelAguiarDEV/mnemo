#!/bin/bash
# Mnemo — SessionStart hook for Claude Code
#
# 1. Ensures the mnemo server is running
# 2. Creates a session in mnemo
# 3. Auto-imports git-synced chunks if .mnemo/manifest.json exists
# 4. Injects Memory Protocol instructions + memory context

MNEMO_PORT="${MNEMO_PORT:-7437}"
MNEMO_URL="http://127.0.0.1:${MNEMO_PORT}"

# Read hook input from stdin
INPUT=$(cat)
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // empty')
CWD=$(echo "$INPUT" | jq -r '.cwd // empty')
PROJECT=$(basename "$CWD")

# Ensure mnemo server is running
if ! curl -sf "${MNEMO_URL}/health" --max-time 1 > /dev/null 2>&1; then
  mnemo serve &>/dev/null &
  sleep 0.5
fi

# Create session
if [ -n "$SESSION_ID" ] && [ -n "$PROJECT" ]; then
  curl -sf "${MNEMO_URL}/sessions" \
    -X POST \
    -H "Content-Type: application/json" \
    -d "{\"id\":\"${SESSION_ID}\",\"project\":\"${PROJECT}\",\"directory\":\"${CWD}\"}" \
    > /dev/null 2>&1
fi

# Auto-import git-synced chunks
if [ -f "${CWD}/.mnemo/manifest.json" ]; then
  mnemo sync --import 2>/dev/null
fi

# Fetch memory context
CONTEXT=$(curl -sf "${MNEMO_URL}/context?project=${PROJECT}" --max-time 3 2>/dev/null | jq -r '.context // empty')

# Inject Memory Protocol + context — stdout goes to Claude as additionalContext
cat <<'PROTOCOL'
## Mnemo Persistent Memory — ACTIVE PROTOCOL

You have mnemo memory tools (mem_save, mem_search, mem_context, mem_session_summary).
This protocol is MANDATORY and ALWAYS ACTIVE.

### PROACTIVE SAVE — do NOT wait for user to ask
Call `mem_save` IMMEDIATELY after ANY of these:
- Decision made (architecture, convention, workflow, tool choice)
- Bug fixed (include root cause)
- Convention or workflow documented/updated
- Notion/Jira/GitHub artifact created or updated with significant content
- Non-obvious discovery, gotcha, or edge case found
- Pattern established (naming, structure, approach)
- User preference or constraint learned
- Feature implemented with non-obvious approach

**Self-check after EVERY task**: "Did I just make a decision, fix a bug, learn something, or establish a convention? If yes → mem_save NOW."

### SEARCH MEMORY when:
- User asks to recall anything ("remember", "what did we do", "acordate", "qué hicimos")
- Starting work on something that might have been done before
- User mentions a topic you have no context on
- User's FIRST message references the project, a feature, or a problem — call `mem_search` with keywords from their message to check for prior work before responding

### SESSION CLOSE — before saying "done"/"listo":
Call `mem_session_summary` with: Goal, Discoveries, Accomplished, Next Steps, Relevant Files.
PROTOCOL

# Inject memory context if available
if [ -n "$CONTEXT" ]; then
  printf "\n%s\n" "$CONTEXT"
fi

exit 0
