package movegrammar

import (
	"math"
	"testing"
)

func TestParse_SingleForward(t *testing.T) {
	instructions, err := Parse("forward 1m")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(instructions) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(instructions))
	}
	inst := instructions[0]
	assertFloat(t, "velocity", inst.Velocity, 0.2)
	assertFloat(t, "omega", inst.Omega, 0.0)
	assertInt(t, "duration", inst.Duration, 5000)
}

func TestParse_SingleBackward(t *testing.T) {
	instructions, err := Parse("backward 0.5m")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(instructions) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(instructions))
	}
	inst := instructions[0]
	assertFloat(t, "velocity", inst.Velocity, -0.2)
	assertFloat(t, "omega", inst.Omega, 0.0)
	assertInt(t, "duration", inst.Duration, 2500)
}

func TestParse_LeftArc(t *testing.T) {
	instructions, err := Parse("left 1m")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	inst := instructions[0]
	assertFloat(t, "velocity", inst.Velocity, 0.15)
	assertFloat(t, "omega", inst.Omega, 0.5)
	assertInt(t, "duration", inst.Duration, 6667)
}

func TestParse_RightArc(t *testing.T) {
	instructions, err := Parse("right 2m")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	inst := instructions[0]
	assertFloat(t, "velocity", inst.Velocity, 0.15)
	assertFloat(t, "omega", inst.Omega, -0.5)
	assertInt(t, "duration", inst.Duration, 13333)
}

func TestParse_RotatePositive(t *testing.T) {
	instructions, err := Parse("rotate 90")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	inst := instructions[0]
	assertFloat(t, "velocity", inst.Velocity, 0.0)
	assertFloat(t, "omega", inst.Omega, 1.0)
	assertInt(t, "duration", inst.Duration, 1571)
}

func TestParse_RotateNegative(t *testing.T) {
	instructions, err := Parse("rotate -90")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	inst := instructions[0]
	assertFloat(t, "velocity", inst.Velocity, 0.0)
	assertFloat(t, "omega", inst.Omega, -1.0)
	assertInt(t, "duration", inst.Duration, 1571)
}

func TestParse_MultipleInstructions(t *testing.T) {
	instructions, err := Parse("forward 1m, rotate -90, forward 0.5m")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(instructions) != 3 {
		t.Fatalf("expected 3 instructions, got %d", len(instructions))
	}

	// First: forward 1m
	assertFloat(t, "inst[0].velocity", instructions[0].Velocity, 0.2)
	assertFloat(t, "inst[0].omega", instructions[0].Omega, 0.0)
	assertInt(t, "inst[0].duration", instructions[0].Duration, 5000)

	// Second: rotate -90
	assertFloat(t, "inst[1].velocity", instructions[1].Velocity, 0.0)
	assertFloat(t, "inst[1].omega", instructions[1].Omega, -1.0)
	assertInt(t, "inst[1].duration", instructions[1].Duration, 1571)

	// Third: forward 0.5m
	assertFloat(t, "inst[2].velocity", instructions[2].Velocity, 0.2)
	assertFloat(t, "inst[2].omega", instructions[2].Omega, 0.0)
	assertInt(t, "inst[2].duration", instructions[2].Duration, 2500)
}

func TestParse_UnitVariants(t *testing.T) {
	tests := []struct {
		input    string
		duration int
	}{
		{"forward 1m", 5000},
		{"forward 1meter", 5000},
		{"forward 1meters", 5000},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			instructions, err := Parse(tc.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertInt(t, "duration", instructions[0].Duration, tc.duration)
		})
	}
}

func TestParse_WhitespaceHandling(t *testing.T) {
	instructions, err := Parse("  forward 1m ,  rotate 90  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(instructions) != 2 {
		t.Fatalf("expected 2 instructions, got %d", len(instructions))
	}
}

func TestParse_CaseInsensitiveDirection(t *testing.T) {
	instructions, err := Parse("Forward 1m")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertFloat(t, "velocity", instructions[0].Velocity, 0.2)
}

// Error cases

func TestParse_EmptyInput(t *testing.T) {
	_, err := Parse("")
	assertError(t, err, "no movement instructions provided")
}

func TestParse_WhitespaceOnly(t *testing.T) {
	_, err := Parse("   ")
	assertError(t, err, "no movement instructions provided")
}

func TestParse_UnknownDirection(t *testing.T) {
	_, err := Parse("sideways 1m")
	assertError(t, err, "unknown direction \"sideways\"")
}

func TestParse_MissingDistance(t *testing.T) {
	_, err := Parse("forward")
	assertError(t, err, "missing distance for \"forward\"")
}

func TestParse_MissingAngle(t *testing.T) {
	_, err := Parse("rotate")
	assertError(t, err, "missing angle for rotate")
}

func TestParse_ZeroDistance(t *testing.T) {
	_, err := Parse("forward 0m")
	assertError(t, err, "distance must be greater than zero")
}

func TestParse_ZeroRotation(t *testing.T) {
	_, err := Parse("rotate 0")
	assertError(t, err, "rotation angle must not be zero")
}

func TestParse_NegativeDistance(t *testing.T) {
	_, err := Parse("forward -1m")
	assertError(t, err, "distance must be positive")
}

func TestParse_MissingUnit(t *testing.T) {
	_, err := Parse("forward 1")
	assertError(t, err, "missing distance for direction")
}

func TestParse_MalformedNumber(t *testing.T) {
	_, err := Parse("rotate abc")
	assertError(t, err, "could not parse \"abc\"")
}

func TestParse_DurationExceedsLimit(t *testing.T) {
	// forward 7m at 0.2 m/s = 35000ms > 30000ms limit
	_, err := Parse("forward 7m")
	assertError(t, err, "exceeds the 30s limit")
}

func TestParse_RotationDurationExceedsLimit(t *testing.T) {
	// 2000 degrees at 1.0 rad/s = ~34907ms > 30000ms
	_, err := Parse("rotate 2000")
	assertError(t, err, "exceeds the 30s limit")
}

func TestParse_VerySmallDistance(t *testing.T) {
	// 0.01m at 0.2 m/s = 50ms < 100ms minimum
	_, err := Parse("forward 0.01m")
	assertError(t, err, "below the 100ms minimum")
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
