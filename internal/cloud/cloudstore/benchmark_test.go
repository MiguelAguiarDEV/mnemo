package cloudstore

import (
	"fmt"
	"testing"
)

func BenchmarkSearchLargeUserDataset(b *testing.B) {
	cs := newTestStore(b)

	user, err := cs.CreateUser("bench-user", "bench-user@example.com", "password123")
	if err != nil {
		b.Fatalf("CreateUser: %v", err)
	}
	if err := cs.CreateSession(user.ID, "bench-session", "engram", "/bench"); err != nil {
		b.Fatalf("CreateSession: %v", err)
	}

	for i := 0; i < 5000; i++ {
		content := fmt.Sprintf("general benchmark observation %d", i)
		if i%20 == 0 {
			content = fmt.Sprintf("auth benchmark observation %d with token refresh and retry handling", i)
		}
		if _, err := cs.AddObservation(user.ID, AddCloudObservationParams{
			SessionID: "bench-session",
			Type:      "benchmark",
			Title:     fmt.Sprintf("Observation %d", i),
			Content:   content,
			Project:   "engram",
			Scope:     "project",
		}); err != nil {
			b.Fatalf("AddObservation %d: %v", i, err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := cs.Search(user.ID, "auth retry", CloudSearchOptions{Project: "engram", Limit: 20})
		if err != nil {
			b.Fatalf("Search: %v", err)
		}
		if len(results) == 0 {
			b.Fatal("expected benchmark search results")
		}
	}
}
