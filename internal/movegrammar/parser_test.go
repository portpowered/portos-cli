package movegrammar

import (
	"math"
	"testing"
)

func TestParse_SingleForward(t *testing.T) {
	// 1 m needs 3.3 s at 0.3 m/s: four whole seconds at 0.25 m/s.
	instructions, err := Parse("forward 1m", DefaultBounds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(instructions) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(instructions))
	}
	inst := instructions[0]
	assertFloat(t, "velocity", inst.Velocity, 0.25)
	assertFloat(t, "omega", inst.Omega, 0.0)
	assertInt(t, "duration", inst.Duration, 4000)
}

func TestParse_SingleBackward(t *testing.T) {
	instructions, err := Parse("backward 0.5m", DefaultBounds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(instructions) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(instructions))
	}
	inst := instructions[0]
	assertFloat(t, "velocity", inst.Velocity, -0.25)
	assertFloat(t, "omega", inst.Omega, 0.0)
	assertInt(t, "duration", inst.Duration, 2000)
}

func TestParse_LeftArc(t *testing.T) {
	// 1 m on a 0.3 m circle turns 3.33 rad, which needs 4.2 s at 0.8 rad/s: five seconds.
	instructions, err := Parse("left 1m", DefaultBounds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(instructions) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(instructions))
	}
	inst := instructions[0]
	assertFloat(t, "velocity", inst.Velocity, 0.2)
	assertFloat(t, "omega", inst.Omega, 0.6666)
	assertInt(t, "duration", inst.Duration, 5000)
}

func TestParse_RightArc(t *testing.T) {
	// 2 m needs 8.3 s, more than one 5 s move: two equal moves of five seconds.
	instructions, err := Parse("right 2m", DefaultBounds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(instructions) != 2 {
		t.Fatalf("expected 2 instructions, got %d", len(instructions))
	}
	for _, inst := range instructions {
		assertFloat(t, "velocity", inst.Velocity, 0.2)
		assertFloat(t, "omega", inst.Omega, -0.6666)
		assertInt(t, "duration", inst.Duration, 5000)
	}
}

func TestParse_RotatePositive(t *testing.T) {
	instructions, err := Parse("rotate 90", DefaultBounds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(instructions) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(instructions))
	}
	inst := instructions[0]
	assertFloat(t, "velocity", inst.Velocity, 0.0)
	assertFloat(t, "omega", inst.Omega, 0.7853)
	assertInt(t, "duration", inst.Duration, 2000)
}

func TestParse_RotateStaysWithinTheTurnRate(t *testing.T) {
	// The S8 refuses omega above 0.8 rad/s and durations off whole seconds (Spike 3, 2026-11-11).
	for _, tc := range []struct {
		input string
		omega float64
	}{{"rotate 30", 0.5235}, {"rotate 73", 0.6370}, {"rotate 5", 0.0872}} {
		instructions, err := Parse(tc.input, DefaultBounds)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.input, err)
		}
		inst := instructions[0]
		assertFloat(t, tc.input+" omega", inst.Omega, tc.omega)
		if inst.Omega > DefaultBounds.MaxOmega || inst.Duration%DefaultBounds.DurationStepMs != 0 || inst.Duration < DefaultBounds.MinDurationMs {
			t.Fatalf("%s: %+v is outside the bounds", tc.input, inst)
		}
	}
}

func TestParse_RotateNegative(t *testing.T) {
	instructions, err := Parse("rotate -90", DefaultBounds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(instructions) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(instructions))
	}
	inst := instructions[0]
	assertFloat(t, "velocity", inst.Velocity, 0.0)
	assertFloat(t, "omega", inst.Omega, -0.7853)
	assertInt(t, "duration", inst.Duration, 2000)
}

func TestParse_MultipleInstructions(t *testing.T) {
	instructions, err := Parse("forward 1m, rotate -90, forward 0.5m", DefaultBounds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(instructions) != 3 {
		t.Fatalf("expected 3 instructions, got %d", len(instructions))
	}

	// First: forward 1m
	assertFloat(t, "inst[0].velocity", instructions[0].Velocity, 0.25)
	assertInt(t, "inst[0].duration", instructions[0].Duration, 4000)

	// Second: rotate -90
	assertFloat(t, "inst[1].velocity", instructions[1].Velocity, 0.0)
	assertFloat(t, "inst[1].omega", instructions[1].Omega, -0.7853)
	assertInt(t, "inst[1].duration", instructions[1].Duration, 2000)

	// Third: forward 0.5m
	assertFloat(t, "inst[2].velocity", instructions[2].Velocity, 0.25)
	assertInt(t, "inst[2].duration", instructions[2].Duration, 2000)
}

func TestParse_UnitVariants(t *testing.T) {
	tests := []struct {
		input    string
		duration int
	}{
		{"forward 1m", 4000},
		{"forward 1meter", 4000},
		{"forward 1meters", 4000},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			instructions, err := Parse(tc.input, DefaultBounds)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertInt(t, "duration", instructions[0].Duration, tc.duration)
		})
	}
}

func TestParse_WhitespaceHandling(t *testing.T) {
	instructions, err := Parse("  forward 1m ,  rotate 90  ", DefaultBounds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(instructions) != 2 {
		t.Fatalf("expected 2 instructions, got %d", len(instructions))
	}
}

func TestParse_CaseInsensitiveDirection(t *testing.T) {
	instructions, err := Parse("Forward 1m", DefaultBounds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertFloat(t, "velocity", instructions[0].Velocity, 0.25)
}

// Error cases

func TestParse_EmptyInput(t *testing.T) {
	_, err := Parse("", DefaultBounds)
	assertError(t, err, "no movement instructions provided")
}

func TestParse_WhitespaceOnly(t *testing.T) {
	_, err := Parse("   ", DefaultBounds)
	assertError(t, err, "no movement instructions provided")
}

func TestParse_UnknownDirection(t *testing.T) {
	_, err := Parse("sideways 1m", DefaultBounds)
	assertError(t, err, "unknown direction \"sideways\"")
}

func TestParse_MissingDistance(t *testing.T) {
	_, err := Parse("forward", DefaultBounds)
	assertError(t, err, "missing distance for \"forward\"")
}

func TestParse_MissingAngle(t *testing.T) {
	_, err := Parse("rotate", DefaultBounds)
	assertError(t, err, "missing angle for rotate")
}

func TestParse_ZeroDistance(t *testing.T) {
	_, err := Parse("forward 0m", DefaultBounds)
	assertError(t, err, "distance must be greater than zero")
}

func TestParse_ZeroRotation(t *testing.T) {
	_, err := Parse("rotate 0", DefaultBounds)
	assertError(t, err, "rotation angle must not be zero")
}

func TestParse_NegativeDistance(t *testing.T) {
	_, err := Parse("forward -1m", DefaultBounds)
	assertError(t, err, "distance must be positive")
}

func TestParse_MissingUnit(t *testing.T) {
	_, err := Parse("forward 1", DefaultBounds)
	assertError(t, err, "missing distance for direction")
}

func TestParse_MalformedNumber(t *testing.T) {
	_, err := Parse("rotate abc", DefaultBounds)
	assertError(t, err, "could not parse \"abc\"")
}

func TestParse_DurationExceedsLimit(t *testing.T) {
	// 10 m needs seven 5 s moves: 35 s, past the 30 s limit
	_, err := Parse("forward 10m", DefaultBounds)
	assertError(t, err, "exceeds the 30s limit")
}

func TestParse_RotationDurationExceedsLimit(t *testing.T) {
	// 2000 degrees at 0.8 rad/s needs 44 s
	_, err := Parse("rotate 2000", DefaultBounds)
	assertError(t, err, "exceeds the 30s limit")
}

func TestParse_LongPhraseIsSplitEvenly(t *testing.T) {
	// 7 m needs 23.3 s: five moves of five seconds at 0.28 m/s.
	instructions, err := Parse("forward 7m", DefaultBounds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(instructions) != 5 {
		t.Fatalf("expected 5 instructions, got %d", len(instructions))
	}
	for _, inst := range instructions {
		assertFloat(t, "velocity", inst.Velocity, 0.28)
		assertInt(t, "duration", inst.Duration, 5000)
	}
}

func TestParse_ShortPhraseTakesTheShortestDuration(t *testing.T) {
	instructions, err := Parse("forward 0.1m", DefaultBounds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertFloat(t, "velocity", instructions[0].Velocity, 0.1)
	assertInt(t, "duration", instructions[0].Duration, 1000)
}

// Helpers

func assertFloat(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.001 {
		t.Errorf("%s: got %f, want %f", name, got, want)
	}
}

func assertInt(t *testing.T, name string, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %d, want %d", name, got, want)
	}
}

func assertError(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", substr)
	}
	if !contains(err.Error(), substr) {
		t.Errorf("expected error containing %q, got %q", substr, err.Error())
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
