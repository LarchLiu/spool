package workspace

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestUserFixturesUseMinimumBcryptCost(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "fixture")
	var hash string
	if err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id=?`, u.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if cost, err := bcrypt.Cost([]byte(hash)); err != nil || cost != bcrypt.MinCost {
		t.Fatalf("fixture bcrypt cost = %d, want %d: %v", cost, bcrypt.MinCost, err)
	}
	if _, _, err := s.login(u.Username, "test-password-2026"); err != nil {
		t.Fatal("fixture cannot authenticate:", err)
	}
}

func TestSaveUserUsesProductionBcryptCost(t *testing.T) {
	for _, update := range []bool{false, true} {
		name := "create"
		if update {
			name = "update"
		}
		t.Run(name, func(t *testing.T) {
			s := testStore(t)
			input := UserInput{Username: "alice", Nickname: "Alice", Password: "production-password-2026"}
			var oldToken string
			if update {
				u := testUser(t, s, input.Username)
				input.ID = u.ID
				var err error
				oldToken, _, err = s.login(u.Username, "test-password-2026")
				if err != nil {
					t.Fatal(err)
				}
			}
			u, err := s.SaveUser(input)
			if err != nil {
				t.Fatal(err)
			}
			var hash string
			if err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id=?`, u.ID).Scan(&hash); err != nil {
				t.Fatal(err)
			}
			if cost, err := bcrypt.Cost([]byte(hash)); err != nil || cost != 12 {
				t.Fatalf("production bcrypt cost = %d, want 12: %v", cost, err)
			}
			if update {
				// One real-cost comparison covers authentication; fixtures cover the rest.
				if _, _, err := s.login(u.Username, input.Password); err != nil {
					t.Fatal("updated password cannot authenticate:", err)
				}
				if _, err := s.authenticate(oldToken); err == nil {
					t.Fatal("password update did not revoke the old login")
				}
			}
		})
	}
}
