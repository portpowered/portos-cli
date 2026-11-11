package movegrammar

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Bounds is the move range a robot accepts: speeds up to MaxVelocity (m/s) and MaxOmega (rad/s),
// and durations that are whole multiples of DurationStepMs between MinDurationMs and MaxDurationMs.
// It mirrors the remote-control interface configuration a robot reports.
type Bounds struct {
	MaxVelocity    float64
	MaxOmega       float64
	MinDurationMs  int
	MaxDurationMs  int
	DurationStepMs int
}

// DefaultBounds are the Roborock S8's limits, the defaults the navigation helper uses too.
var DefaultBounds = Bounds{MaxVelocity: 0.3, MaxOmega: 0.8, MinDurationMs: 1000, MaxDurationMs: 5000, DurationStepMs: 1000}

const (
	// ArcRadiusMetres is the radius of the circle that left and right drive along.
	ArcRadiusMetres = 0.3
	// MaxPhraseDurationMs is the longest one phrase ("forward 3m") may take, over all its moves.
	MaxPhraseDurationMs = 30000

	millisPerSecond = 1000.0
	// speedDecimals is the precision of the velocity and omega sent.
	speedDecimals = 1e4
	// stepSlack absorbs float error when counting whole steps.
	stepSlack = 1e-9
)

// Instruction represents a single parsed movement instruction with concrete
// velocity, omega, and duration values ready for remote-control move messages.
type Instruction struct {
	Velocity float64 `json:"velocity"`
	Omega    float64 `json:"omega"`
	Duration int     `json:"duration"`
}

// Parse converts a move grammar string (e.g., "forward 1m, rotate 90, backward 0.5m") into the
// moves that carry it out within bounds. Each phrase becomes one or more moves of whole duration
// steps at no more than the top speeds; a phrase longer than one move allows is split evenly.
// Returns an error for malformed input or a phrase that would take more than 30 s.
func Parse(input string, bounds Bounds) ([]Instruction, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, fmt.Errorf("no movement instructions provided")
	}

	parts := strings.Split(trimmed, ",")
	instructions := make([]Instruction, 0, len(parts))

	for _, part := range parts {
		phrase, err := parseInstruction(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		moves, err := phrase.plan(bounds)
		if err != nil {
			return nil, err
		}
		instructions = append(instructions, moves...)
	}

	return instructions, nil
}

// motion is one phrase as a signed distance in metres and a signed turn in radians (positive is left).
type motion struct{ metres, radians float64 }

func parseInstruction(s string) (motion, error) {
	if s == "" {
		return motion{}, fmt.Errorf("no movement instructions provided")
	}

	tokens := strings.Fields(s)
	direction := strings.ToLower(tokens[0])

	switch direction {
	case "forward", "backward", "left", "right":
		if len(tokens) < 2 {
			return motion{}, fmt.Errorf("missing distance for %q: expected a number followed by m, meter, or meters", direction)
		}
		distance, err := parseDistance(tokens[1])
		if err != nil {
			return motion{}, err
		}
		switch direction {
		case "backward":
			return motion{metres: -distance}, nil
		case "left":
			return motion{metres: distance, radians: distance / ArcRadiusMetres}, nil
		case "right":
			return motion{metres: distance, radians: -distance / ArcRadiusMetres}, nil
		}
		return motion{metres: distance}, nil
	case "rotate":
		return parseRotation(tokens[1:])
	default:
		return motion{}, fmt.Errorf("unknown direction %q: expected forward, backward, left, right, or rotate", direction)
	}
}

func parseRotation(tokens []string) (motion, error) {
	if len(tokens) == 0 {
		return motion{}, fmt.Errorf("missing angle for rotate: expected a number in degrees")
	}
	degrees, err := strconv.ParseFloat(tokens[0], 64)
	if err != nil {
		return motion{}, fmt.Errorf("could not parse %q: expected a number", tokens[0])
	}
	if degrees == 0 {
		return motion{}, fmt.Errorf("rotation angle must not be zero")
	}
	return motion{radians: degrees * math.Pi / 180}, nil
}

// plan splits the motion into the fewest equal moves that fit bounds at the top speeds, each a
// whole number of duration steps, with the speeds scaled to cover exactly its share.
func (m motion) plan(b Bounds) ([]Instruction, error) {
	step := max(b.DurationStepMs, 1)
	seconds := max(math.Abs(m.metres)/b.MaxVelocity, math.Abs(m.radians)/b.MaxOmega)
	parts := max(1, int(math.Ceil(seconds*millisPerSecond/float64(b.MaxDurationMs)-stepSlack)))
	steps := max(1, int(math.Ceil(seconds*millisPerSecond/float64(parts)/float64(step)-stepSlack)))
	duration := min(max(steps*step, b.MinDurationMs), b.MaxDurationMs)
	if total := parts * duration; total > MaxPhraseDurationMs {
		return nil, fmt.Errorf("instruction would take %dms which exceeds the 30s limit; reduce the distance or angle", total)
	}
	perSecond := float64(parts) * float64(duration) / millisPerSecond
	move := Instruction{Velocity: rounded(m.metres / perSecond), Omega: rounded(m.radians / perSecond), Duration: duration}
	moves := make([]Instruction, parts)
	for i := range moves {
		moves[i] = move
	}
	return moves, nil
}

// rounded keeps speedDecimals precision, rounding toward zero so a speed never passes its bound.
func rounded(v float64) float64 { return math.Trunc(v*speedDecimals) / speedDecimals }

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
