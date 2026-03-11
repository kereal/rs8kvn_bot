package utils

import (
	"regexp"
	"testing"
)

func TestGenerateUUID(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "generates non-empty UUID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateUUID()
			if got == "" {
				t.Error("GenerateUUID() returned empty string")
			}
		})
	}
}

func TestGenerateUUID_Format(t *testing.T) {
	uuid := GenerateUUID()

	// UUID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	// Pattern: 8 hex chars - 4 hex chars - 4 hex chars - 4 hex chars - 12 hex chars
	pattern := `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`
	matched, err := regexp.MatchString(pattern, uuid)
	if err != nil {
		t.Fatalf("Failed to compile regex: %v", err)
	}

	if !matched {
		t.Errorf("GenerateUUID() = %s, expected format xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx", uuid)
	}
}

func TestGenerateUUID_Uniqueness(t *testing.T) {
	const iterations = 1000
	uuids := make(map[string]bool, iterations)

	for i := 0; i < iterations; i++ {
		uuid := GenerateUUID()
		if uuids[uuid] {
			t.Errorf("GenerateUUID() generated duplicate UUID: %s", uuid)
		}
		uuids[uuid] = true
	}
}

func TestGenerateSubID(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "generates non-empty SubID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateSubID()
			if got == "" {
				t.Error("GenerateSubID() returned empty string")
			}
		})
	}
}

func TestGenerateSubID_Format(t *testing.T) {
	subID := GenerateSubID()

	// SubID format: 14 hex characters
	pattern := `^[0-9a-f]{14}$`
	matched, err := regexp.MatchString(pattern, subID)
	if err != nil {
		t.Fatalf("Failed to compile regex: %v", err)
	}

	if !matched {
		t.Errorf("GenerateSubID() = %s, expected 14 hex characters", subID)
	}
}

func TestGenerateSubID_Uniqueness(t *testing.T) {
	const iterations = 1000
	subIDs := make(map[string]bool, iterations)

	for i := 0; i < iterations; i++ {
		subID := GenerateSubID()
		if subIDs[subID] {
			t.Errorf("GenerateSubID() generated duplicate SubID: %s", subID)
		}
		subIDs[subID] = true
	}
}

func TestGenerateSubID_Length(t *testing.T) {
	subID := GenerateSubID()
	expectedLen := 14

	if len(subID) != expectedLen {
		t.Errorf("GenerateSubID() length = %d, expected %d", len(subID), expectedLen)
	}
}
