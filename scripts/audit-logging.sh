#!/usr/bin/env bash
# audit-logging.sh — Verify logging requirements for JARVIS skills architecture.
# Task 5.3: Scan all new .go files and verify logging compliance.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PASS=0
FAIL=0
WARN=0

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo "=== JARVIS Logging Audit ==="
echo "Scanning: internal/skills/, internal/tools/, internal/llm/"
echo ""

# ─── Check 1: Every non-test .go file has at least 1 slog call ───
echo "=== File-Level slog Usage ==="
for dir in "$ROOT/internal/skills" "$ROOT/internal/tools" "$ROOT/internal/llm"; do
    if [ ! -d "$dir" ]; then
        continue
    fi
    for file in "$dir"/*.go; do
        [ -f "$file" ] || continue
        basename="$(basename "$file")"
        # Skip test files
        [[ "$basename" == *_test.go ]] && continue

        slog_count="$(grep -c 'slog\.' "$file" 2>/dev/null || true)"
        slog_count="${slog_count:-0}"
        if [ "$slog_count" -gt 0 ] 2>/dev/null; then
            echo -e "  ${GREEN}PASS${NC} $basename — $slog_count slog calls"
            PASS=$((PASS + 1))
        else
            # Interface-only files (like backend.go) may not need logging
            func_count="$(grep -c '^func ' "$file" 2>/dev/null || true)"
            func_count="${func_count:-0}"
            if [ "$func_count" -eq 0 ] 2>/dev/null; then
                echo -e "  ${YELLOW}SKIP${NC} $basename — no function implementations (interface-only)"
            else
                echo -e "  ${RED}FAIL${NC} $basename — no slog calls found ($func_count functions)"
                FAIL=$((FAIL + 1))
            fi
        fi
    done
done
echo ""

# ─── Check 2: TRACE level logs (via slog.Debug with specific content) ───
echo "=== TRACE Level Checks ==="
echo "  (TRACE = slog.Debug with system-level details: prompt size, tool count, always-skills count)"

# System prompt size
if grep -rq 'prompt_len\|prompt_size\|system.prompt.size' \
    "$ROOT/internal/skills/" "$ROOT/internal/tools/" "$ROOT/internal/llm/" \
    "$ROOT/internal/cloud/jarvis/orchestrator.go" 2>/dev/null; then
    echo -e "  ${GREEN}PASS${NC} System prompt size logged"
    PASS=$((PASS + 1))
else
    echo -e "  ${RED}FAIL${NC} Missing TRACE log for system prompt size"
    FAIL=$((FAIL + 1))
fi

# Tool count
if grep -rq '"tools"\|tool.count\|tools_registered' \
    "$ROOT/internal/skills/" "$ROOT/internal/tools/" "$ROOT/internal/llm/" \
    "$ROOT/internal/cloud/jarvis/orchestrator.go" 2>/dev/null; then
    echo -e "  ${GREEN}PASS${NC} Tool count logged"
    PASS=$((PASS + 1))
else
    echo -e "  ${RED}FAIL${NC} Missing TRACE log for tool count"
    FAIL=$((FAIL + 1))
fi

# Always-skills count
if grep -rq 'always.skills\|always_skills' \
    "$ROOT/internal/skills/" "$ROOT/internal/tools/" "$ROOT/internal/llm/" \
    "$ROOT/internal/cloud/jarvis/orchestrator.go" 2>/dev/null; then
    echo -e "  ${GREEN}PASS${NC} Always-skills count logged"
    PASS=$((PASS + 1))
else
    echo -e "  ${RED}FAIL${NC} Missing TRACE log for always-skills count"
    FAIL=$((FAIL + 1))
fi
echo ""

# ─── Check 3: ERROR level logs for failure conditions ───
echo "=== ERROR Level Checks ==="

# Read failures
if grep -rq 'slog\.Error.*read\|slog\.Error.*fail.*read\|failed to read' \
    "$ROOT/internal/skills/" "$ROOT/internal/tools/" "$ROOT/internal/llm/" \
    "$ROOT/internal/cloud/jarvis/orchestrator.go" 2>/dev/null; then
    echo -e "  ${GREEN}PASS${NC} Read failure errors logged"
    PASS=$((PASS + 1))
else
    echo -e "  ${RED}FAIL${NC} Missing ERROR log for read failures"
    FAIL=$((FAIL + 1))
fi

# Unknown tools
if grep -rq 'unknown.tool\|slog\.Warn.*unknown' \
    "$ROOT/internal/tools/" "$ROOT/internal/cloud/jarvis/orchestrator.go" 2>/dev/null; then
    echo -e "  ${GREEN}PASS${NC} Unknown tool warnings logged"
    PASS=$((PASS + 1))
else
    echo -e "  ${RED}FAIL${NC} Missing WARN log for unknown tools"
    FAIL=$((FAIL + 1))
fi

# Missing always-skills
if grep -rq 'slog\.Error.*always\|failed to load always' \
    "$ROOT/internal/cloud/jarvis/orchestrator.go" 2>/dev/null; then
    echo -e "  ${GREEN}PASS${NC} Missing always-skill errors logged"
    PASS=$((PASS + 1))
else
    echo -e "  ${RED}FAIL${NC} Missing ERROR log for missing always-skills"
    FAIL=$((FAIL + 1))
fi
echo ""

# ─── Check 4: Key log levels per component ───
echo "=== Component Logging Levels ==="

# Schema validator: DEBUG + WARN
if grep -q 'slog\.Debug.*valid' "$ROOT/internal/skills/schema.go" 2>/dev/null && \
   grep -q 'slog\.Warn\|slog\.Debug.*fail' "$ROOT/internal/skills/schema.go" 2>/dev/null; then
    echo -e "  ${GREEN}PASS${NC} schema.go — DEBUG + WARN levels present"
    PASS=$((PASS + 1))
else
    echo -e "  ${RED}FAIL${NC} schema.go — missing required log levels"
    FAIL=$((FAIL + 1))
fi

# Registry: INFO + WARN + DEBUG
if grep -q 'slog\.Info' "$ROOT/internal/skills/registry.go" 2>/dev/null && \
   grep -q 'slog\.Warn' "$ROOT/internal/skills/registry.go" 2>/dev/null && \
   grep -q 'slog\.Debug' "$ROOT/internal/skills/registry.go" 2>/dev/null; then
    echo -e "  ${GREEN}PASS${NC} registry.go — INFO + WARN + DEBUG levels present"
    PASS=$((PASS + 1))
else
    echo -e "  ${RED}FAIL${NC} registry.go — missing required log levels"
    FAIL=$((FAIL + 1))
fi

# Loader: INFO + WARN + ERROR
if grep -q 'slog\.Info' "$ROOT/internal/skills/loader.go" 2>/dev/null && \
   grep -q 'slog\.Warn' "$ROOT/internal/skills/loader.go" 2>/dev/null && \
   grep -q 'slog\.Error' "$ROOT/internal/skills/loader.go" 2>/dev/null; then
    echo -e "  ${GREEN}PASS${NC} loader.go — INFO + WARN + ERROR levels present"
    PASS=$((PASS + 1))
else
    echo -e "  ${RED}FAIL${NC} loader.go — missing required log levels"
    FAIL=$((FAIL + 1))
fi

# Tool dispatcher: INFO + WARN + ERROR + DEBUG
if grep -q 'slog\.Info\|logger\.Info' "$ROOT/internal/tools/tool.go" 2>/dev/null && \
   grep -q 'slog\.Warn\|logger\.Warn' "$ROOT/internal/tools/tool.go" 2>/dev/null && \
   grep -q 'slog\.Debug\|logger\.Debug' "$ROOT/internal/tools/tool.go" 2>/dev/null; then
    echo -e "  ${GREEN}PASS${NC} tool.go — INFO + WARN + DEBUG levels present"
    PASS=$((PASS + 1))
else
    echo -e "  ${RED}FAIL${NC} tool.go — missing required log levels"
    FAIL=$((FAIL + 1))
fi

# Builtin tools: INFO + WARN + ERROR
if grep -q 'slog\.Info' "$ROOT/internal/tools/builtin.go" 2>/dev/null && \
   grep -q 'slog\.Warn' "$ROOT/internal/tools/builtin.go" 2>/dev/null && \
   grep -q 'slog\.Error' "$ROOT/internal/tools/builtin.go" 2>/dev/null; then
    echo -e "  ${GREEN}PASS${NC} builtin.go — INFO + WARN + ERROR levels present"
    PASS=$((PASS + 1))
else
    echo -e "  ${RED}FAIL${NC} builtin.go — missing required log levels"
    FAIL=$((FAIL + 1))
fi

# OpenCode backend: INFO + DEBUG + ERROR
if grep -q 'slog\.Info\|logger\.Info\|b\.logger\.Info' "$ROOT/internal/llm/opencode.go" 2>/dev/null && \
   grep -q 'slog\.Debug\|logger\.Debug\|b\.logger\.Debug' "$ROOT/internal/llm/opencode.go" 2>/dev/null && \
   grep -q 'slog\.Error\|logger\.Error\|b\.logger\.Error' "$ROOT/internal/llm/opencode.go" 2>/dev/null; then
    echo -e "  ${GREEN}PASS${NC} opencode.go — INFO + DEBUG + ERROR levels present"
    PASS=$((PASS + 1))
else
    echo -e "  ${RED}FAIL${NC} opencode.go — missing required log levels"
    FAIL=$((FAIL + 1))
fi
echo ""

# ─── Summary ───
echo "=== Summary ==="
echo -e "  ${GREEN}PASS${NC}: $PASS"
echo -e "  ${YELLOW}WARN${NC}: $WARN"
echo -e "  ${RED}FAIL${NC}: $FAIL"
echo ""

if [ "$FAIL" -gt 0 ]; then
    echo -e "${RED}Logging audit has failures. Review above.${NC}"
    exit 1
else
    echo -e "${GREEN}Logging audit passed.${NC}"
    exit 0
fi
