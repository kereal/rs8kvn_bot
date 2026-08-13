package utils

import (
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerators_Contract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		generate func() (string, error)
		length   int
		valid    func(string) bool
	}{
		{
			name:     "uuid v4",
			generate: GenerateUUID,
			length:   36,
			valid: func(value string) bool {
				return regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(value)
			},
		},
		{
			name:     "subscription id",
			generate: GenerateSubID,
			length:   10,
			valid: func(value string) bool {
				if len(value) != 10 {
					return false
				}

				for _, char := range value {
					if !strings.ContainsRune("0123456789abcdef", char) {
						return false
					}
				}

				return true
			},
		},
		{
			name:     "invite code",
			generate: GenerateInviteCode,
			length:   8,
			valid: func(value string) bool {
				if len(value) != 8 {
					return false
				}

				for _, char := range value {
					if !strings.ContainsRune("0123456789abcdefghijklmnopqrstuvwxyz", char) {
						return false
					}
				}

				return true
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := tt.generate()
			require.NoError(t, err)
			require.Len(t, value, tt.length)
			require.True(t, tt.valid(value), "generated value has invalid format: %q", value)
		})
	}
}

func TestGenerators_Concurrent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		generate func() (string, error)
	}{
		{name: "uuid", generate: GenerateUUID},
		{name: "subscription id", generate: GenerateSubID},
		{name: "invite code", generate: GenerateInviteCode},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const (
				workers         = 8
				valuesPerWorker = 25
			)

			values := make(chan string, workers*valuesPerWorker)
			errs := make(chan error, workers*valuesPerWorker)

			var wg sync.WaitGroup
			wg.Add(workers)

			for range workers {
				go func() {
					defer wg.Done()

					for range valuesPerWorker {
						value, err := tt.generate()
						if err != nil {
							errs <- err
							continue
						}

						values <- value
					}
				}()
			}

			wg.Wait()
			close(values)
			close(errs)

			for err := range errs {
				require.NoError(t, err)
			}

			seen := make(map[string]struct{}, workers*valuesPerWorker)
			for value := range values {
				seen[value] = struct{}{}
			}

			require.Len(t, seen, workers*valuesPerWorker, "concurrent generator returned duplicate values")
		})
	}
}

func BenchmarkGenerateUUID(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, err := GenerateUUID()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGenerateSubID(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, err := GenerateSubID()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGenerateInviteCode(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, err := GenerateInviteCode()
		if err != nil {
			b.Fatal(err)
		}
	}
}
