package statestore

import "testing"

func TestAddAIUsage_Accumulates(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if err := AddAIUsage("recap", AIUsageCounts{Calls: 1, InputTokens: 100, OutputTokens: 10, CostUSD: 0.01}, 1000); err != nil {
		t.Fatalf("AddAIUsage: %v", err)
	}
	if err := AddAIUsage("recap", AIUsageCounts{Calls: 1, InputTokens: 200, OutputTokens: 20, CostUSD: 0.02}, 2000); err != nil {
		t.Fatalf("AddAIUsage: %v", err)
	}
	if err := AddAIUsage("naming", AIUsageCounts{Calls: 1, InputTokens: 50, OutputTokens: 5, CostUSD: 0.03}, 3000); err != nil {
		t.Fatalf("AddAIUsage: %v", err)
	}

	s, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s == nil || s.AIUsage == nil {
		t.Fatal("expected AIUsage recorded")
	}
	u := s.AIUsage
	if u.SinceTS != 1000 {
		t.Errorf("SinceTS: got %d want 1000 (first call)", u.SinceTS)
	}
	if u.Total.Calls != 3 || u.Total.InputTokens != 350 || u.Total.OutputTokens != 35 {
		t.Errorf("Total: got %+v", u.Total)
	}
	if got := u.ByTask["recap"]; got.Calls != 2 || got.InputTokens != 300 {
		t.Errorf("ByTask[recap]: got %+v", got)
	}
	if got := u.ByTask["naming"]; got.Calls != 1 || got.InputTokens != 50 {
		t.Errorf("ByTask[naming]: got %+v", got)
	}
}

func TestAddAIUsage_ZeroDeltaNoOps(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := AddAIUsage("recap", AIUsageCounts{}, 1000); err != nil {
		t.Fatalf("AddAIUsage: %v", err)
	}
	s, _ := Load()
	if s != nil && s.AIUsage != nil {
		t.Errorf("zero delta should not record usage, got %+v", s.AIUsage)
	}
}

func TestResetAIUsage(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := AddAIUsage("recap", AIUsageCounts{Calls: 5, InputTokens: 999}, 1000); err != nil {
		t.Fatalf("AddAIUsage: %v", err)
	}
	if err := ResetAIUsage(5000); err != nil {
		t.Fatalf("ResetAIUsage: %v", err)
	}
	s, _ := Load()
	if s == nil || s.AIUsage == nil {
		t.Fatal("expected AIUsage present after reset")
	}
	if s.AIUsage.Total.Calls != 0 || len(s.AIUsage.ByTask) != 0 {
		t.Errorf("reset should zero counters, got %+v", s.AIUsage)
	}
	if s.AIUsage.SinceTS != 5000 {
		t.Errorf("SinceTS: got %d want 5000", s.AIUsage.SinceTS)
	}
}
