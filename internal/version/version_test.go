package version

import "testing"

func TestString(t *testing.T) {
	tests := []struct {
		name           string
		version        string
		commit         string
		expectedOutput string
	}{
		{
			name:           "default values",
			version:        "dev",
			commit:         "unknown",
			expectedOutput: "dev",
		},
		{
			name:           "empty commit",
			version:        "1.0.0",
			commit:         "",
			expectedOutput: "1.0.0",
		},
		{
			name:           "full commit hash",
			version:        "1.0.0",
			commit:         "abc123def456789",
			expectedOutput: "1.0.0 (abc123d)",
		},
		{
			name:           "short commit (7 chars)",
			version:        "1.0.0",
			commit:         "abc123d",
			expectedOutput: "1.0.0 (abc123d)",
		},
		{
			name:           "very short commit (less than 7 chars)",
			version:        "1.0.0",
			commit:         "abc",
			expectedOutput: "1.0.0 (abc)",
		},
		{
			name:           "single char commit",
			version:        "1.0.0",
			commit:         "a",
			expectedOutput: "1.0.0 (a)",
		},
		{
			name:           "exactly 7 chars",
			version:        "2.0.0",
			commit:         "1234567",
			expectedOutput: "2.0.0 (1234567)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original values
			origVersion := Version
			origCommit := Commit
			defer func() {
				Version = origVersion
				Commit = origCommit
			}()

			// Set test values
			Version = tt.version
			Commit = tt.commit

			result := String()
			if result != tt.expectedOutput {
				t.Errorf("String() = %q, want %q", result, tt.expectedOutput)
			}
		})
	}
}

func TestStringNoPanic(t *testing.T) {
	// This test specifically ensures no panic occurs with edge cases
	origVersion := Version
	origCommit := Commit
	defer func() {
		Version = origVersion
		Commit = origCommit
	}()

	edgeCases := []struct {
		version string
		commit  string
	}{
		{"1.0.0", ""},
		{"1.0.0", "a"},
		{"1.0.0", "ab"},
		{"1.0.0", "abc"},
		{"1.0.0", "abcd"},
		{"1.0.0", "abcde"},
		{"1.0.0", "abcdef"},
		{"1.0.0", "abcdefg"},
		{"1.0.0", "abcdefgh"},
	}

	for _, tc := range edgeCases {
		t.Run("commit_len_"+string(rune(len(tc.commit))+'0'), func(t *testing.T) {
			Version = tc.version
			Commit = tc.commit

			// Should not panic
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("String() panicked with commit=%q: %v", tc.commit, r)
				}
			}()

			result := String()
			if result == "" {
				t.Error("String() returned empty string")
			}
		})
	}
}

