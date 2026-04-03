#!/bin/bash
# Mnemo — Post-compaction hook for Claude Code
#
# When compaction happens, inject Memory Protocol + context and instruct
# the agent to persist the compacted summary via mem_session_summary.

MNEMO_PORT="${MNEMO_PORT:-7437}"
MNEMO_URL="http://127.0.0.1:${MNEMO_PORT}"

# Read hook input from stdin
INPUT=$(cat)
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // empty')
CWD=$(echo "$INPUT" | jq -r '.cwd // empty')
PROJECT=$(basename "$CWD")

# Ensure session exists
if [ -n "$SESSION_ID" ] && [ -n "$PROJECT" ]; then
  curl -sf "${MNEMO_URL}/sessions" \
    -X POST \
    -H "Content-Type: application/json" \
    -d "{\"id\":\"${SESSION_ID}\",\"project\":\"${PROJECT}\",\"directory\":\"${CWD}\"}" \
    > /dev/null 2>&1
fi

# Fetch context from previous sessions
CONTEXT=$(curl -sf "${MNEMO_URL}/context?project=${PROJECT}" --max-time 3 2>/dev/null | jq -r '.context // empty')

# Inject Memory Protocol + compaction instruction + context
cat <<PROTOCOL
## Mnemo Persistent Memory — ACTIVE PROTOCOL

You have mnemo memory tools (mem_save, mem_search, mem_context, mem_session_summary).
This protocol is MANDATORY and ALWAYS ACTIVE.

### PROACTIVE SAVE — do NOT wait for user to ask
Call \`mem_save\` IMMEDIATELY after ANY of these:
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

### SESSION CLOSE — before saying "done"/"listo":
Call \`mem_session_summary\` with: Goal, Discoveries, Accomplished, Next Steps, Relevant Files.

---

CRITICAL INSTRUCTION POST-COMPACTION — follow these steps IN ORDER:

1. FIRST: Call mem_session_summary with the content of the compacted summary above. Use project: '${PROJECT}'.
   This preserves what was accomplished before compaction.

2. THEN: Call mem_context with project: '${PROJECT}' to recover recent session history and observations.
   Read the returned context carefully — it tells you what was being worked on.

3. If you need more detail on a specific topic, call mem_search with relevant keywords.

4. Only THEN continue working on what the user asked.

All 4 steps are MANDATORY. Without them, you lose context and start blind.

${CONTEXT}
PROTOCOL

exit 0
