package gateway

// Intent router — fast-path matching of natural language DMs to programmatic
// cmd* handlers, bypassing the LLM for common queries like "cuánto me queda?",
// "uso", "tareas", etc. Slash commands still work unchanged; this adds a
// zero-cost path for free-form DMs that match a known intent.

import (
	"log/slog"
	"regexp"
	"strings"
)

// intentRoute pairs a set of regex patterns with a handler that produces the
// final response. When any regex matches (case-insensitive) on the user's
// message, the router calls the handler directly, bypassing the LLM.
type intentRoute struct {
	name    string
	regexes []*regexp.Regexp
	handler func(dc *DiscordChannel) (string, error)
}

// mustRegex compiles a regex with (?i) prefix for case-insensitive matching.
// Panics at startup if any pattern is malformed (bugs caught early).
func mustRegex(p string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)` + p)
}

// defaultIntents returns the built-in intent routing table.
// Order matters: the first match wins, so put more specific patterns first.
var defaultIntents = []intentRoute{
	{
		name: "usage",
		regexes: []*regexp.Regexp{
			mustRegex(`^/usage\??$`),
			mustRegex(`cu[aá]nto.{0,20}(me.{0,5})?queda`),
			mustRegex(`^uso\??$`),
			mustRegex(`dime.{0,10}(el.{0,5})?uso`),
			mustRegex(`(?:^|\s)uso(?:\s|$|\?|!|\.)`),
			mustRegex(`^l[ií]mites?\??$`),
			mustRegex(`rate.?limit`),
			mustRegex(`claude.{0,20}(uso|l[ií]mite)`),
			mustRegex(`how much.{0,20}left`),
			mustRegex(`^usage\??$`),
		},
		handler: func(dc *DiscordChannel) (string, error) { return dc.cmdUsage() },
	},
	{
		name: "health",
		regexes: []*regexp.Regexp{
			mustRegex(`^/health\??$`),
			mustRegex(`^salud\??$`),
			mustRegex(`^health\??$`),
			mustRegex(`estado.{0,20}(del.{0,5})?(sistema|stack)`),
			mustRegex(`^c[oó]mo est[aá]s\??$`),
			mustRegex(`^how are you\??$`),
			mustRegex(`^status\??$`),
		},
		handler: func(dc *DiscordChannel) (string, error) { return dc.cmdHealth() },
	},
	{
		name: "tasks",
		regexes: []*regexp.Regexp{
			mustRegex(`^/tasks\??$`),
			mustRegex(`^tareas\??$`),
			mustRegex(`^tasks\??$`),
			mustRegex(`lista.{0,10}tareas`),
			mustRegex(`^list tasks\??$`),
			mustRegex(`^qu[eé] tareas`),
			mustRegex(`^pending tasks\??$`),
		},
		handler: func(dc *DiscordChannel) (string, error) { return dc.cmdTasks("") },
	},
	{
		name: "costs",
		regexes: []*regexp.Regexp{
			mustRegex(`^/costs\??$`),
			mustRegex(`^cost[eo]s?\??$`),
			mustRegex(`cu[aá]nto.{0,10}(he.{0,5})?gastado`),
			mustRegex(`costo.{0,10}mensual`),
			mustRegex(`monthly.{0,5}cost`),
		},
		handler: func(dc *DiscordChannel) (string, error) { return dc.cmdCosts() },
	},
	{
		name: "services",
		regexes: []*regexp.Regexp{
			mustRegex(`^/services\??$`),
			mustRegex(`^servicios\??$`),
			mustRegex(`^services\??$`),
			mustRegex(`^contenedores\??$`),
			mustRegex(`^containers\??$`),
			mustRegex(`^docker ps\??$`),
		},
		handler: func(dc *DiscordChannel) (string, error) { return dc.cmdServices() },
	},
	{
		name: "stats",
		regexes: []*regexp.Regexp{
			mustRegex(`^/stats\??$`),
			mustRegex(`^stats\??$`),
			mustRegex(`^estad[ií]sticas\??$`),
			mustRegex(`historial.{0,10}uso`),
		},
		handler: func(dc *DiscordChannel) (string, error) { return dc.cmdStats(7) },
	},
	{
		name: "help",
		regexes: []*regexp.Regexp{
			mustRegex(`^/help\??$`),
			mustRegex(`^ayuda\??$`),
			mustRegex(`^help\??$`),
			mustRegex(`qu[eé] puedes hacer`),
			mustRegex(`comandos disponibles`),
			mustRegex(`what can you do`),
		},
		handler: func(dc *DiscordChannel) (string, error) { return dc.cmdHelp(), nil },
	},
}

// matchIntent returns the first intent route whose regex matches the trimmed
// message. Returns nil if no intent matches (caller should fall through to
// the LLM).
func (dc *DiscordChannel) matchIntent(message string) *intentRoute {
	text := strings.TrimSpace(message)
	if text == "" || len(text) > 200 {
		// Skip empty or unreasonably long messages — those are conversations,
		// not commands.
		return nil
	}
	for i := range defaultIntents {
		route := &defaultIntents[i]
		for _, re := range route.regexes {
			if re.MatchString(text) {
				if dc.logger != nil {
					dc.logger.Info("intent matched", "intent", route.name, "text_len", len(text))
				} else {
					slog.Info("intent matched", "intent", route.name, "text_len", len(text))
				}
				return route
			}
		}
	}
	return nil
}
