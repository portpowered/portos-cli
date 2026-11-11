package movegrammar

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Default speeds for movement instructions.
const (
	DefaultLinearSpeed    = 0.2  // m/s for forward/backward
	DefaultArcLinearSpeed = 0.15 // m/s linear component during arc (left/right)
	DefaultArcOmega       = 0.5  // rad/s angular component during arc (left/right)
	DefaultRotationOmega  = 1.0  // rad/s for pure rotation

	MaxDurationMs = 30000 // 30 seconds
	MinDurationMs = 100   // 100 milliseconds
)

// Instruction represents a single parsed movement instruction with concrete
// velocity, omega, and duration values ready for remote-control move messages.
type Instruction struct {
	Velocity float64 `json:"velocity"`
	Omega    float64 `json:"omega"`
	Duration int     `json:"duration"`
}

// Parse converts a move grammar string (e.g., "forward 1m, rotate 90, backward 0.5m")
// into a slice of Instructions. Returns an error for malformed input.
func Parse(input string) ([]Instruction, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, fmt.Errorf("no movement instructions provided")
	}

	parts := strings.Split(trimmed, ",")
	instructions := make([]Instruction, 0, len(parts))

	for _, part := range parts {
		inst, err := parseInstruction(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		instructions = append(instructions, inst)
	}

	return instructions, nil
}

func parseInstruction(s string) (Instruction, error) {
	if s == "" {
		return Instruction{}, fmt.Errorf("no movement instructions provided")
	}

	tokens := strings.Fields(s)
	direction := strings.ToLower(tokens[0])

	switch direction {
	case "forward", "backward":
		return parseLinear(direction, tokens[1:])
	case "left", "right":
		return parseArc(direction, tokens[1:])
	case "rotate":
		return parseRotation(tokens[1:])
	default:
		return Instruction{}, fmt.Errorf("unknown direction %q: expected forward, backward, left, right, or rotate", direction)
	}
}

func parseLinear(direction string, tokens []string) (Instruction, error) {
	if len(tokens) == 0 {
		return Instruction{}, fmt.Errorf("missing distance for %q: expected a number followed by m, meter, or meters", direction)
	}

	distance, err := parseDistance(tokens[0])
	if err != nil {
		return Instruction{}, err
	}

	durationMs := distance / DefaultLinearSpeed * 1000
	if err := validateDuration(durationMs); err != nil {
		return Instruction{}, err
	}

	velocity := DefaultLinearSpeed
	if direction == "backward" {
		velocity = -DefaultLinearSpeed
	}

	return Instruction{
		Velocity: velocity,
		Omega:    0.0,
		Duration: int(math.Round(durationMs)),
	}, nil
}

func parseArc(direction string, tokens []string) (Instruction, error) {
	if len(tokens) == 0 {
		return Instruction{}, fmt.Errorf("missing distance for %q: expected a number followed by m, meter, or meters", direction)
	}

	distance, err := parseDistance(tokens[0])
	if err != nil {
		return Instruction{}, err
	}

	durationMs := distance / DefaultArcLinearSpeed * 1000
	if err := validateDuration(durationMs); err != nil {
		return Instruction{}, err
	}

	omega := DefaultArcOmega
	if direction == "right" {
		omega = -DefaultArcOmega
	}

	return Instruction{
		Velocity: DefaultArcLinearSpeed,
		Omega:    omega,
		Duration: int(math.Round(durationMs)),
	}, nil
}

func parseRotation(tokens []string) (Instruction, error) {
	if len(tokens) == 0 {
		return Instruction{}, fmt.Errorf("missing angle for rotate: expected a number in degrees")
	}

	degrees, err := strconv.ParseFloat(tokens[0], 64)
	if err != nil {
		return Instruction{}, fmt.Errorf("could not parse %q: expected a number", tokens[0])
	}

	if degrees == 0 {
		return Instruction{}, fmt.Errorf("rotation angle must not be zero")
	}

	omega := DefaultRotationOmega
	if degrees < 0 {
		omega = -DefaultRotationOmega
	}

	durationMs := math.Abs(degrees) * (math.Pi / 180) / DefaultRotationOmega * 1000
	if err := validateDuration(durationMs); err != nil {
		return Instruction{}, err
	}

	return Instruction{
		Velocity: 0.0,
		Omega:    omega,
		Duration: int(math.Round(durationMs)),
	}, nil
}

// parseDistance extracts the numeric value from a distance token like "1m", "0.5meter", "2meters".
func parseDistance(token string) (float64, error) {
	// Strip unit suffix
	num := token
	switch {
	case strings.HasSuffix(token, "meters"):
		num = strings.TrimSuffix(token, "meters")
	case strings.HasSuffix(token, "meter"):
		num = strings.TrimSuffix(token, "meter")
	case strings.HasSuffix(token, "m"):
		num = strings.TrimSuffix(token, "m")
	default:
		return 0, fmt.Errorf("missing distance for direction: expected a number followed by m, meter, or meters")
	}

	distance, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("could not parse %q: expected a number", token)
	}

	if distance < 0 {
		return 0, fmt.Errorf("distance must be positive; use \"backward\" for reverse movement")
	}

	if distance == 0 {
		return 0, fmt.Errorf("distance must be greater than zero")
	}

	return distance, nil
}

func validateDuration(durationMs float64) error {
	rounded := int(math.Round(durationMs))
	if rounded > MaxDurationMs {
		return fmt.Errorf("instruction would take %dms which exceeds the 30s limit; reduce the distance", rounded)
	}
	if rounded < MinDurationMs {
		return fmt.Errorf("instruction would take %dms which is below the 100ms minimum; increase the distance", rounded)
	}
	return nil
}
