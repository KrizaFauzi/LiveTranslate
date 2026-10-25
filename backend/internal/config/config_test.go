package config

import "testing"

func TestSupabaseRejectsUnsafeOrigins(t *testing.T) {
	for _, raw := range []string{
		"http://supabase.example.com",
		"https://user@supabase.example.com",
		"https://supabase.example.com/rest/v1",
		"https://supabase.example.com?token=secret",
		"https://supabase.example.com#fragment",
	} {
		t.Run(raw, func(t *testing.T) {
			_, err := (Config{SupabaseURL: raw, SupabaseKey: "sb_publishable_test"}).Supabase()
			if err == nil {
				t.Fatalf("unsafe Supabase origin accepted: %s", raw)
			}
		})
	}
}

func TestSupabaseAllowsHTTPSAndLoopbackHTTPOrigins(t *testing.T) {
	for _, raw := range []string{
		"https://project.supabase.co",
		"http://127.0.0.1:54321",
		"http://localhost:54321",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := (Config{SupabaseURL: raw, SupabaseKey: "sb_publishable_test"}).Supabase(); err != nil {
				t.Fatalf("valid Supabase origin rejected: %s: %v", raw, err)
			}
		})
	}
}
