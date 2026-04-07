package gateway

import (
	"log/slog"
	"testing"
)

func TestMatchIntent(t *testing.T) {
	dc := &DiscordChannel{logger: slog.Default()}
	cases := []struct {
		text string
		want string // intent name or "" for no match
	}{
		{"cuánto me queda?", "usage"},
		{"cuanto me queda", "usage"},
		{"uso", "usage"},
		{"uso?", "usage"},
		{"limites", "usage"},
		{"límites", "usage"},
		{"how much usage left", "usage"},
		{"usage", "usage"},
		{"salud", "health"},
		{"health?", "health"},
		{"cómo estás", "health"},
		{"how are you", "health"},
		{"status", "health"},
		{"estado del sistema", "health"},
		{"tareas", "tasks"},
		{"tasks", "tasks"},
		{"lista de tareas", "tasks"},
		{"costes", "costs"},
		{"costo", "costs"},
		{"cuánto he gastado este mes", "costs"},
		{"monthly cost", "costs"},
		{"servicios", "services"},
		{"containers", "services"},
		{"docker ps", "services"},
		{"stats", "stats"},
		{"estadísticas", "stats"},
		{"ayuda", "help"},
		{"help", "help"},
		{"qué puedes hacer", "help"},
		{"what can you do", "help"},
		// Should NOT match — fall through to LLM
		{"explícame cómo funciona JARVIS", ""},
		{"hola", ""},
		{"cuéntame un chiste", ""},
		{"", ""},
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			got := dc.matchIntent(c.text)
			gotName := ""
			if got != nil {
				gotName = got.name
			}
			if gotName != c.want {
				t.Errorf("matchIntent(%q) = %q, want %q", c.text, gotName, c.want)
			}
		})
	}
}
