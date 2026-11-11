"""Starter helper for robot tricks: a small layer over the `port` CLI.

Copy it next to your tricks and improve it. It needs Python 3.8+ and nothing outside the
standard library. Every robot command goes through `port`; never call Port OS or the robot
directly while a hold exists.

    from portos_tricks import Robot

    with Robot("my-vacuum") as robot:      # holds the robot; leaving the block releases it (docks)
        start = robot.pose()
        robot.drive_to(start.x + 1.0, start.y)
        robot.turn_by(90)
        robot.checkpoint("arrived", pose=robot.pose())

If PORTOS_LEASE is already set (you ran `port session hold` yourself), Robot uses that hold and
leaves it open when the block ends: whoever opens a hold releases it.
"""

import json
import math
import os
import subprocess
import sys
import time
from dataclasses import dataclass, asdict
from typing import List, Optional, Sequence, Tuple

# Exit classes of the `port` CLI.
EXIT_INVALID = 2
EXIT_AUTH = 3
EXIT_NOT_CARRIED_OUT = 10
EXIT_SESSION_NOT_ACTIVE = 11
EXIT_BUSY = 12
EXIT_HOLD = 13
EXIT_CAMERA = 14

# The robot rejects a turn in place under 5 degrees and a drive under 5 cm as not carried out.
MIN_TURN_DEGREES = 5.0
MIN_DRIVE_METRES = 0.05
# A straight drive that turned more than this went around something: re-aim before the next leg.
BEND_DEGREES = 15.0


class RobotError(Exception):
    """A `port` command failed. code is the CLI's exit code, detail its error message."""

    def __init__(self, code: int, detail: str, pose: "Optional[Pose]" = None):
        super().__init__(detail)
        self.code = code
        self.detail = detail
        self.pose = pose


class NotCarriedOut(RobotError):
    """The robot did not do what the move asked: an obstacle, a wall, its dock, or a stall."""


class SessionNotActive(RobotError):
    """The remote-control session ended and the robot is back on its dock."""


class Busy(RobotError):
    """Another move or a talkback clip is still running."""


class HoldProblem(RobotError):
    """No lease, another hold's lease, or the hold has ended (the detail says why)."""


class CameraUnavailable(RobotError):
    """The camera refused, needs a PIN, or could not connect."""


class NotArrived(Exception):
    """drive_to used up its moves before reaching the point."""

    def __init__(self, target: Tuple[float, float], pose: "Pose"):
        super().__init__("not within tolerance of %s after the allowed moves; now at %s" % (target, pose))
        self.target = target
        self.pose = pose


_ERRORS = {
    EXIT_NOT_CARRIED_OUT: NotCarriedOut,
    EXIT_SESSION_NOT_ACTIVE: SessionNotActive,
    EXIT_BUSY: Busy,
    EXIT_HOLD: HoldProblem,
    EXIT_CAMERA: CameraUnavailable,
}


@dataclass
class Pose:
    """Where the robot is: metres in the map frame, heading in degrees (0 is +x, counter-clockwise)."""

    x: float
    y: float
    heading: Optional[float]
    frame: str = ""
    room: str = ""

    @staticmethod
    def from_attribute(value: dict) -> "Pose":
        x, y = value["point"]["coordinates"][:2]
        return Pose(
            x=float(x),
            y=float(y),
            heading=None if value.get("heading") is None else float(value["heading"]),
            frame=value.get("relativeToGroupRouteId", ""),
            room=value.get("containedInRoomGroupId", ""),
        )


@dataclass
class MoveReply:
    """A measured move: the pose after it, the straight-line distance and the signed turn."""

    pose: Pose
    distance: float
    turned: float


@dataclass
class Bounds:
    """Move limits from the robot's remote-control configuration (Roborock S8 values by default)."""

    max_velocity: float = 0.3
    max_omega: float = 0.8
    min_duration_ms: int = 1000
    max_duration_ms: int = 5000
    duration_step_ms: int = 1000


def angle_diff(target: float, current: float) -> float:
    """The signed turn in (-180, 180] that takes heading current to heading target."""
    diff = (target - current) % 360.0
    return diff - 360.0 if diff > 180.0 else diff


def bearing(from_x: float, from_y: float, to_x: float, to_y: float) -> float:
    """The heading in [0, 360) that faces from one point to another."""
    return math.degrees(math.atan2(to_y - from_y, to_x - from_x)) % 360.0


def _whole_steps(seconds_needed: float, bounds: Bounds) -> int:
    """The shortest allowed duration in ms that fits seconds_needed at the top rate."""
    step = bounds.duration_step_ms
    steps = max(1, math.ceil(seconds_needed * 1000.0 / step - 1e-9))
    return max(bounds.min_duration_ms, min(bounds.max_duration_ms, steps * step))


def turn_plan(degrees: float, bounds: Bounds = Bounds()) -> List[Tuple[float, int]]:
    """Moves (omega rad/s, duration ms) that turn in place by degrees (positive is left).

    Durations are whole steps, so the remainder goes into a scaled omega: 73 degrees is
    omega 0.637 for 2000 ms. A turn longer than one move allows is split evenly.
    """
    radians = math.radians(degrees)
    if abs(degrees) < 1e-9:
        return []
    longest = bounds.max_omega * bounds.max_duration_ms / 1000.0
    parts = max(1, math.ceil(abs(radians) / longest - 1e-9))
    plan = []
    for _ in range(parts):
        share = radians / parts
        duration = _whole_steps(abs(share) / bounds.max_omega, bounds)
        plan.append((round(share / (duration / 1000.0), 4), duration))
    return plan


def drive_plan(metres: float, bounds: Bounds = Bounds()) -> Tuple[float, int]:
    """One straight move (velocity m/s, duration ms) that covers metres, at most one move long."""
    longest = bounds.max_velocity * bounds.max_duration_ms / 1000.0
    metres = max(-longest, min(longest, metres))
    duration = _whole_steps(abs(metres) / bounds.max_velocity, bounds)
    return round(metres / (duration / 1000.0), 4), duration


class Port:
    """Runs the `port` CLI. cli is the binary; config_dir is passed as --config-dir when set."""

    def __init__(self, cli: str = "port", config_dir: str = "", env: Optional[dict] = None):
        self.cli = cli
        self.config_dir = config_dir
        self.env = dict(os.environ if env is None else env)

    def run(self, args: Sequence[str]) -> Tuple[int, str, str]:
        command = [self.cli] + (["--config-dir", self.config_dir] if self.config_dir else []) + list(args)
        done = subprocess.run(command, capture_output=True, text=True, env=self.env)
        return done.returncode, done.stdout, done.stderr

    def json(self, args: Sequence[str]) -> dict:
        """Runs a command with --json and returns its output, raising the typed error on failure."""
        code, out, err = self.run(list(args) + ["--json"])
        if code != 0:
            raise _error(code, out, err)
        return json.loads(out) if out.strip() else {}


def _error(code: int, out: str, err: str) -> RobotError:
    detail = ""
    try:
        detail = _first_message(json.loads(out))
    except ValueError:
        pass
    if not detail:
        lines = [line for line in (err or out).strip().splitlines() if line.strip()]
        detail = lines[-1] if lines else "port exited %d" % code
    return _ERRORS.get(code, RobotError)(code, detail)


def _first_message(value) -> str:
    """The first error message in a JSON reply, wherever the CLI put it."""
    if isinstance(value, dict):
        for key in ("message", "detail", "error"):
            if isinstance(value.get(key), str) and value[key]:
                return value[key]
        for child in value.values():
            found = _first_message(child)
            if found:
                return found
    elif isinstance(value, list):
        for child in value:
            found = _first_message(child)
            if found:
                return found
    return ""


class Robot:
    """One held robot. Use it in a with block so the hold is released (and the robot docks) even
    when the trick fails."""

    def __init__(self, endpoint: str, port: Optional[Port] = None, bounds: Bounds = Bounds(),
                 dead_man: str = "", log=None):
        self.endpoint = endpoint
        self.port = port or Port()
        self.bounds = bounds
        self.dead_man = dead_man
        self.hold_id = ""
        self.owns_hold = False
        self.last_pose: Optional[Pose] = None
        self.trail: List[Tuple[float, float]] = []
        self.replies: List[dict] = []
        self.log = log if log is not None else sys.stderr
        self._snapshot_at = 0.0

    # Hold and release.

    def hold(self) -> "Robot":
        """Opens a motion hold, unless PORTOS_LEASE already names one."""
        if self.port.env.get("PORTOS_LEASE"):
            return self
        args = ["session", "hold", self.endpoint, "--motion"]
        if self.dead_man:
            args += ["--dead-man", self.dead_man]
        opened = self.port.json(args)
        self.hold_id, self.owns_hold = opened["holdId"], True
        self.port.env["PORTOS_LEASE"] = opened["lease"]
        if opened.get("pose"):
            self._seen(Pose.from_attribute(opened["pose"]))
        return self

    def release(self) -> None:
        """Releases a hold this Robot opened: the session ends and the robot returns to its dock."""
        if not self.owns_hold:
            return
        self.owns_hold = False
        code, out, err = self.port.run(["session", "release", self.hold_id, "--json"])
        self.port.env.pop("PORTOS_LEASE", None)
        if code != 0:
            raise _error(code, out, err)

    def __enter__(self) -> "Robot":
        return self.hold()

    def __exit__(self, kind, error, trace) -> None:
        try:
            self.release()
        except RobotError as failure:
            if error is None:
                raise
            print("release failed: %s" % failure, file=self.log)

    # Reading.

    def pose(self) -> Pose:
        """Reads the pose from the robot (a forced read, a few seconds)."""
        value = self.port.json(["endpoint", "attribute", self.endpoint, "spatial/pose", "--force"])
        return self._seen(Pose.from_attribute(value.get("value", value)))

    def where(self) -> Pose:
        """The newest pose a move reply gave, or a fresh read when there is none."""
        return self.last_pose if self.last_pose is not None else self.pose()

    def _seen(self, pose: Pose) -> Pose:
        self.last_pose = pose
        self.trail.append((pose.x, pose.y))
        return pose

    # Moving.

    def move(self, velocity: float, omega: float, duration_ms: int) -> MoveReply:
        """Sends one move and returns the measured reply. A failure raises its typed error, with
        the pose read afterwards when the robot did not do what was asked."""
        body = json.dumps({"velocity": velocity, "omega": omega, "duration": int(duration_ms)})
        try:
            out = self.port.json(["message", "remote-control/move", self.endpoint, "--wait", "--body", body])
        except NotCarriedOut as failure:
            failure.pose = self.pose()
            raise
        reply = _move_body(out)
        self.replies.append(reply)
        return MoveReply(
            pose=self._seen(Pose.from_attribute(reply["pose"])),
            distance=float(reply.get("distanceMetres", 0.0)),
            turned=float(reply.get("turnedDegrees", 0.0)),
        )

    def _turn_move(self, omega: float, duration_ms: int) -> MoveReply:
        # A turn sent right after a snapshot is sometimes ignored; send it once more.
        try:
            return self.move(0.0, omega, duration_ms)
        except NotCarriedOut:
            if time.time() - self._snapshot_at > 10:
                raise
            time.sleep(2)
            return self.move(0.0, omega, duration_ms)

    def turn_by(self, degrees: float, attempts: int = 3) -> float:
        """Turns in place by degrees (positive is left), correcting from the measured turns.
        Returns the total measured turn; it is within 5 degrees of the ask."""
        turned = 0.0
        for _ in range(attempts):
            remaining = degrees - turned
            if abs(remaining) < MIN_TURN_DEGREES:
                break
            for omega, duration in turn_plan(remaining, self.bounds):
                turned += self._turn_move(omega, duration).turned
        return turned

    def turn_to(self, heading: float, attempts: int = 3) -> Pose:
        """Turns in place to face heading (degrees), correcting from the measured pose."""
        for _ in range(attempts):
            current = self.where().heading
            if current is None:
                raise RobotError(EXIT_SESSION_NOT_ACTIVE, "the robot reports no heading (docked?); hold it first")
            remaining = angle_diff(heading, current)
            if abs(remaining) < MIN_TURN_DEGREES:
                break
            for omega, duration in turn_plan(remaining, self.bounds):
                self._turn_move(omega, duration)
        return self.where()

    def forward(self, metres: float) -> MoveReply:
        """Drives straight for metres (negative reverses), in one move of at most the longest allowed."""
        velocity, duration = drive_plan(metres, self.bounds)
        return self.move(velocity, 0.0, duration)

    def drive_to(self, x: float, y: float, tolerance: float = 0.10, leg: float = 1.0,
                 max_moves: int = 0) -> Pose:
        """Aims at (x, y), drives at most leg metres, re-aims from the measured pose, and repeats
        until within tolerance. A stall raises NotCarriedOut (its pose is where the robot stopped);
        running out of moves raises NotArrived."""
        start = self.where()
        allowed = max_moves or 4 + 3 * math.ceil(math.hypot(x - start.x, y - start.y) / leg)
        for _ in range(allowed):
            here = self.where()
            distance = math.hypot(x - here.x, y - here.y)
            if distance <= max(tolerance, MIN_DRIVE_METRES):
                return here
            want = bearing(here.x, here.y, x, y)
            if here.heading is None or abs(angle_diff(want, here.heading)) >= MIN_TURN_DEGREES:
                self.turn_to(want)
            reply = self.forward(min(distance, leg))
            if abs(reply.turned) > BEND_DEGREES:
                print("bent %.0f degrees around something; re-aiming" % reply.turned, file=self.log)
        here = self.where()
        if math.hypot(x - here.x, y - here.y) <= max(tolerance, MIN_DRIVE_METRES):
            return here
        raise NotArrived((x, y), here)

    # Looking.

    def snapshot(self, path: str) -> str:
        """Saves the camera's current frame to path."""
        code, out, err = self.port.run(["message", "snapshot", self.endpoint, "--output", path])
        self._snapshot_at = time.time()
        if code != 0:
            raise _error(code, out, err)
        return path

    def look_around(self, step_degrees: float = 45.0, directory: str = ".") -> List[dict]:
        """Turns a full circle in steps, taking a frame at each. Returns [{path, heading}]."""
        os.makedirs(directory, exist_ok=True)
        frames = []
        steps = max(1, round(360.0 / step_degrees))
        for index in range(steps):
            heading = self.where().heading
            path = os.path.join(directory, "look-%02d.jpg" % index)
            frames.append({"path": self.snapshot(path), "heading": heading})
            if index < steps - 1:
                self.turn_by(360.0 / steps)
        return frames

    # Handing back.

    def checkpoint(self, name: str, **data) -> dict:
        """Prints one JSON line for you to judge, with the pose, and returns it."""
        record = {"checkpoint": name}
        record.update({key: _plain(value) for key, value in data.items()})
        record.setdefault("pose", _plain(self.where()))
        print(json.dumps(record), flush=True)
        return record


def _move_body(out: dict) -> dict:
    """The move reply body from `port message ... --json` output."""
    results = out.get("results") or []
    if results:
        return results[0]["message"]["body"]
    return out["message"]["body"]


def _plain(value):
    if isinstance(value, Pose):
        return asdict(value)
    if isinstance(value, (list, tuple)):
        return [_plain(item) for item in value]
    return value
