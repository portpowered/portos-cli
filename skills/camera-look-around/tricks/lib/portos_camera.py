"""Starter helper for camera tricks: a small layer over the `port` CLI for a pan-tilt camera.

Copy it next to your tricks and improve it. It needs Python 3.8+ and nothing outside the
standard library. Every camera command goes through `port`.

    from portos_camera import Camera

    with Camera("ring-camera") as camera:   # shared hold; leaving the block releases it
        camera.anchor()                     # left and bottom ends, so angles are exact
        camera.look_at(pan=170, tilt=40)
        camera.snapshot("sofa.jpg")
        camera.move("RIGHT", 0.5)           # a quick nudge, answered in under a second

If PORTOS_LEASE is already set (you ran `port session hold` yourself), Camera uses that hold and
leaves it open when the block ends: whoever opens a hold releases it.

Angles: pan counts degrees right of the left end and tilt counts degrees up from the bottom end,
once the camera has been turned to that end in this camera session (`anchor()` does both). Before
that they count from the ends as the camera's last session left them (`pan_remembered` /
`tilt_remembered`), or from where the session started, and `pan_known` / `tilt_known` are False.
A remembered angle holds only if nothing else turned the camera between sessions: take a
snapshot, and when it shows what your notes say is at that angle, call `trust_remembered()` to
use it without anchoring.

Two ways to turn: `turn` goes by angle in whole steps of about 15 degrees and counts them, so it
is the exact one; `move` turns continuously for a time (or to the end, or until `stop`), so it is
the fast one, and the angles it reports are estimated from time and speed. Anchor again to correct
any drift.
"""

import json
import os
import subprocess
import sys
import time
from dataclasses import dataclass, asdict
from typing import List, Optional, Sequence, Tuple

# Exit classes of the `port` CLI.
EXIT_INVALID = 2
EXIT_BUSY = 12
EXIT_HOLD = 13
EXIT_CAMERA = 14

# A turn of 360 degrees runs the camera to the end of its range in that direction.
TO_END = 360
# A continuous move of 60 seconds, the longest there is, runs the camera to the end of its range.
TO_END_SECONDS = 60
# Degrees per second of a continuous turn, at the low end of a Ring camera's measured rates (pan
# about 34 to 36, tilt 39 to 60). glide() turns by angle with them; the angles a move reports come
# from the same rates in the Ring plugin.
TURN_RATES = {"PAN": 34.0, "TILT": 42.0}
# The camera's view in degrees, wide and tall, measured on a Ring pan-tilt camera. follow() turns
# by these to bring what moved toward the middle of the frame.
VIEW_DEGREES = (130.0, 64.0)

# Gestures: short timed turns, (direction, seconds), that end about where they started. A stroke
# under 0.2 s barely shows; each answers in about half a second once the camera has stopped. Every
# stroke overruns by the time its stop takes to reach the camera, so each gesture has as many
# strokes one way as the other and the overruns cancel. Each stroke waits for the camera to stop
# before the next: sent back to back, Ring once dropped a reversal and the camera ran 40 degrees on
# while the plugin counted it as still.
GESTURES = {
    "nod": [("DOWN", 0.4), ("UP", 0.4), ("DOWN", 0.4), ("UP", 0.4)],
    "quick-nod": [("DOWN", 0.2), ("UP", 0.2)] * 3,
    "big-nod": [("DOWN", 0.8), ("UP", 0.8), ("DOWN", 0.8), ("UP", 0.8)],
    "shake": [("LEFT", 0.3), ("RIGHT", 0.3), ("RIGHT", 0.3), ("LEFT", 0.3)],
    "jiggle": [("LEFT", 0.25), ("RIGHT", 0.25), ("RIGHT", 0.25), ("LEFT", 0.25)],
    "look-up-down": [("UP", 1.0), ("DOWN", 1.0), ("DOWN", 1.0), ("UP", 1.0)],
}
# Degrees either side of the middle of the pan range that a patrol checks.
PATROL_SPREAD = 90.0


class CameraError(Exception):
    """A `port` command failed. code is the CLI's exit code, detail its error message."""

    def __init__(self, code: int, detail: str):
        super().__init__(detail)
        self.code = code
        self.detail = detail


class HoldProblem(CameraError):
    """No lease, another hold's lease, or the hold has ended: hold the camera again."""


class CameraUnavailable(CameraError):
    """The shared camera session ended under you, or the camera refused."""


_ERRORS = {EXIT_HOLD: HoldProblem, EXIT_CAMERA: CameraUnavailable}


@dataclass
class Position:
    """Where the camera points, from a step-ptz reply."""

    pan: float
    tilt: float
    pan_range: float
    tilt_range: float
    pan_known: bool
    tilt_known: bool
    moved: float = 0.0
    pan_moving: bool = False
    tilt_moving: bool = False
    pan_remembered: bool = False
    tilt_remembered: bool = False

    @staticmethod
    def from_reply(value: dict) -> "Position":
        return Position(
            pan=float(value["panDegrees"]),
            tilt=float(value["tiltDegrees"]),
            pan_range=float(value["panRangeDegrees"]),
            tilt_range=float(value["tiltRangeDegrees"]),
            pan_known=value["panReference"] == "LEFT_END",
            tilt_known=value["tiltReference"] == "BOTTOM_END",
            moved=float(value.get("movedDegrees", 0)),
            pan_moving=bool(value.get("panMoving", False)),
            tilt_moving=bool(value.get("tiltMoving", False)),
            pan_remembered=value["panReference"] == "REMEMBERED_LEFT_END",
            tilt_remembered=value["tiltReference"] == "REMEMBERED_BOTTOM_END",
        )


def turns_to(current: Position, pan: Optional[float] = None, tilt: Optional[float] = None,
             slack: float = 5.0) -> List[Tuple[str, float]]:
    """The turns that bring the camera from current to the given angles, each as (direction,
    degrees). Angles are clamped to the camera's range; a difference under slack is skipped. Pan
    and tilt need a known reference (turn to an end first); a target on an unknown axis raises."""
    plan = []
    for target, value, span, known, (up, down), name in (
            (pan, current.pan, current.pan_range, current.pan_known, ("RIGHT", "LEFT"), "pan"),
            (tilt, current.tilt, current.tilt_range, current.tilt_known, ("UP", "DOWN"), "tilt")):
        if target is None:
            continue
        if not known:
            raise ValueError("%s is counted from the session start; turn to an end first (anchor())" % name)
        delta = min(max(target, 0.0), span) - value
        if abs(delta) < slack:
            continue
        plan.append((up if delta > 0 else down, round(abs(delta))))
    return plan


def scan_angles(span: float, every: float) -> List[float]:
    """Evenly spaced angles from 0 to span, at most every degrees apart, ends included."""
    if span <= 0 or every <= 0:
        return [0.0]
    count = max(1, -int(-span // every))
    return [round(span * i / count) for i in range(count + 1)]


def axis_of(direction: str) -> str:
    return "TILT" if direction in ("UP", "DOWN") else "PAN"


def room(position: Optional[Position], direction: str) -> Optional[float]:
    """Degrees the camera can still turn in direction, or None while that axis counts from the
    session start. A remembered axis counts: a wrong guess only makes a gesture run into an end."""
    if position is None:
        return None
    if axis_of(direction) == "PAN":
        value, span, known = position.pan, position.pan_range, position.pan_known or position.pan_remembered
    else:
        value, span, known = position.tilt, position.tilt_range, position.tilt_known or position.tilt_remembered
    if not known:
        return None
    return span - value if direction in ("RIGHT", "UP") else value


def fit_gesture(strokes: Sequence[Tuple[str, float]], position: Optional[Position]) -> List[Tuple[str, float]]:
    """The gesture's strokes, with a turn away from an end first (and back after) when a stroke
    would otherwise run into it, so a nod at the bottom end still looks like a nod and the camera
    ends where it began. An axis that is not anchored is played as it is."""
    plan = list(strokes)
    for axis, (up, down) in (("PAN", ("RIGHT", "LEFT")), ("TILT", ("UP", "DOWN"))):
        below, above = room(position, down), room(position, up)
        if below is None or above is None:
            continue
        rate, reach, low, high = TURN_RATES[axis], 0.0, 0.0, 0.0
        for direction, seconds in strokes:
            if axis_of(direction) == axis:
                reach += seconds * rate if direction == up else -seconds * rate
                low, high = min(low, reach), max(high, reach)
        shift = max(-low - below, 0.0) - max(high - above, 0.0)
        if abs(shift) >= 1:
            away, back = (up, down) if shift > 0 else (down, up)
            plan = [(away, abs(shift) / rate)] + plan + [(back, abs(shift) / rate)]
    return plan


def motion_center(before: bytes, after: bytes, width: int, height: int, threshold: int = 24,
                  least: float = 0.01) -> Optional[Tuple[float, float]]:
    """Where two grey frames of width x height bytes differ: the centre of the changed pixels as
    (x, y) from -0.5 (left, top) to 0.5 (right, bottom), or None when less than least of the
    frame changed."""
    xs, ys = [], []
    for index, (a, b) in enumerate(zip(before, after)):
        if abs(a - b) > threshold:
            xs.append(index % width)
            ys.append(index // width)
    if len(xs) < max(1, least * width * height):
        return None
    return (sum(xs) / len(xs) / max(width - 1, 1) - 0.5, sum(ys) / len(ys) / max(height - 1, 1) - 0.5)


def follow_turns(center: Tuple[float, float], view: Tuple[float, float] = VIEW_DEGREES,
                 gain: float = 0.6, deadband: float = 0.12) -> List[Tuple[str, float]]:
    """The turns, (direction, degrees), that bring a point at center toward the middle of the
    frame. A gain under 1 leaves the rest to the next look, so following never overshoots; an
    offset under deadband of the frame is left alone."""
    plan = []
    for offset, span, (positive, negative) in ((center[0], view[0], ("RIGHT", "LEFT")),
                                               (center[1], view[1], ("DOWN", "UP"))):
        if abs(offset) < deadband:
            continue
        plan.append((positive if offset > 0 else negative, round(abs(offset) * span * gain)))
    return plan


def grey_frame(path: str, width: int = 64, height: int = 36) -> bytes:
    """The image at path as width x height grey bytes, decoded by ffmpeg."""
    done = subprocess.run(["ffmpeg", "-v", "error", "-i", path, "-vf", "scale=%d:%d,format=gray" % (width, height),
                           "-f", "rawvideo", "-"], capture_output=True)
    if done.returncode != 0 or len(done.stdout) != width * height:
        raise CameraError(0, "ffmpeg could not read %s" % path)
    return done.stdout


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
            raise _ERRORS.get(code, CameraError)(code, error_detail(err or out) or "port exited %d" % code)
        return json.loads(out) if out.strip() else {}


def error_detail(text: str) -> str:
    """The message of a failed command's output: error.message from its --json error body, or its
    last line when the output is not JSON."""
    try:
        return str(json.loads(text)["error"]["message"])
    except (ValueError, KeyError, TypeError):
        lines = [line for line in text.strip().splitlines() if line.strip()]
        return lines[-1] if lines else ""


class Camera:
    """One held pan-tilt camera. Use it in a with block so a hold it opened is released."""

    def __init__(self, endpoint: str, port: Optional[Port] = None, log=None):
        self.endpoint = endpoint
        self.port = port or Port()
        self.log = log or sys.stdout
        self.hold_id = ""
        self.opened = False
        self.position: Optional[Position] = None
        # trusted takes remembered angles as known, once a snapshot has confirmed them.
        self.trusted = False

    def hold(self) -> "Camera":
        """Attaches next to any call with a shared hold, unless PORTOS_LEASE is already set."""
        if not self.port.env.get("PORTOS_LEASE"):
            held = self.port.json(["session", "hold", self.endpoint, "--camera", "--shared"])
            self.port.env["PORTOS_LEASE"] = held["lease"]
            self.hold_id, self.opened = held["holdId"], True
        return self

    def release(self) -> None:
        if self.opened:
            self.port.json(["session", "release", self.hold_id])
            self.port.env.pop("PORTOS_LEASE", None)
            self.opened = False

    def __enter__(self) -> "Camera":
        return self.hold()

    def __exit__(self, kind, error, trace) -> None:
        try:
            self.release()
        except CameraError as failure:
            if error is None:
                raise
            print("release failed: %s" % failure, file=self.log)

    def turn(self, direction: str, degrees: Optional[float] = None) -> Position:
        """Turns LEFT, RIGHT, UP or DOWN by one step, or about degrees (TO_END runs to the end)."""
        args = ["message", "step-ptz", self.endpoint, direction]
        if degrees:
            args += ["--degrees", "%g" % min(max(degrees, 1), TO_END)]
        return self._position(self.port.json(args))

    def move(self, direction: str, seconds: Optional[float] = None, speed: Optional[float] = None) -> Position:
        """Turns LEFT, RIGHT, UP or DOWN continuously, at speed (0.1 to 1, default full). With
        seconds (0.1 to 60) it returns once the camera has stopped; TO_END_SECONDS runs to the
        end. Without seconds it returns as soon as the camera turns, and the camera keeps turning
        until stop(), another move on that axis, or the end of its range. A pan and a tilt can
        run at once."""
        args = ["message", "move-ptz", self.endpoint, direction]
        if seconds:
            args += ["--seconds", "%g" % min(max(seconds, 0.1), TO_END_SECONDS)]
        if speed:
            args += ["--speed", "%g" % min(max(speed, 0.1), 1)]
        return self._position(self.port.json(args))

    def stop(self, axis: Optional[str] = None) -> Position:
        """Stops a continuous move on PAN or TILT, or on both, and returns where the camera stopped."""
        args = ["message", "stop-ptz", self.endpoint]
        if axis:
            args += ["--axis", axis]
        return self._position(self.port.json(args))

    def to_end(self, direction: str) -> Position:
        """Runs the camera to the end of its range in direction: the fast way to an end."""
        return self.move(direction, TO_END_SECONDS)

    def _position(self, reply: dict) -> Position:
        if not reply.get("position"):
            raise CameraError(0, "the camera reported no position after the turn")
        self.position = Position.from_reply(reply["position"])
        if self.trusted:
            self.position.pan_known = self.position.pan_known or self.position.pan_remembered
            self.position.tilt_known = self.position.tilt_known or self.position.tilt_remembered
        return self.position

    def where(self) -> Position:
        """Asks where the camera points without turning it (stop-ptz turns nothing when nothing
        turns). At the start of a session the angles may be remembered from the last one."""
        return self.stop()

    def trust_remembered(self) -> Position:
        """Takes the angles the last session left as known, so look_at() and glide_to() use them
        without anchoring. Call it only after a snapshot showed what your notes say is at those
        angles: something else may have turned the camera between sessions. Anchoring again
        replaces them with exact ones."""
        self.trusted = True
        return self.where()

    def anchor(self, pan: bool = True, tilt: bool = True) -> Position:
        """Runs to the left end and the bottom end, so later angles are exact."""
        if pan:
            self.to_end("LEFT")
        if tilt:
            self.to_end("DOWN")
        return self.position

    def look_at(self, pan: Optional[float] = None, tilt: Optional[float] = None) -> Position:
        """Points the camera at absolute angles, anchoring an axis first when it is not known."""
        if self.position is None or (pan is not None and not self.position.pan_known) or \
                (tilt is not None and not self.position.tilt_known):
            self.anchor(pan=pan is not None and not (self.position and self.position.pan_known),
                        tilt=tilt is not None and not (self.position and self.position.tilt_known))
        for direction, degrees in turns_to(self.position, pan, tilt):
            self.turn(direction, degrees)
        return self.position

    def snapshot(self, path: str) -> str:
        """Saves the newest frame from the hold to path."""
        self.port.json(["message", "snapshot", self.endpoint, "--output", path])
        return path

    def panorama(self, out: str, every: float = 60.0, tilt: Optional[float] = None) -> List[dict]:
        """Sweeps the full pan range left to right, saving a frame every few degrees. Returns
        [{path, pan, tilt}] for you to look at; the camera ends at the right end."""
        os.makedirs(out, exist_ok=True)
        self.look_at(pan=0, tilt=tilt)
        frames = []
        for angle in scan_angles(self.position.pan_range, every):
            self.look_at(pan=angle)
            path = os.path.join(out, "pan-%03d.jpg" % round(self.position.pan))
            frames.append({"path": self.snapshot(path), "pan": self.position.pan, "tilt": self.position.tilt})
        return frames

    def glide(self, direction: str, degrees: float) -> Position:
        """Turns about degrees in one continuous move: faster than turn(), which steps, and as exact
        as the turn rates. Answers once the camera has stopped; under a degree it does not turn."""
        if degrees < 1:
            return self.position
        return self.move(direction, degrees / TURN_RATES[axis_of(direction)])

    def gesture(self, name: str) -> Position:
        """Plays one of GESTURES (nod, quick-nod, big-nod, shake, jiggle, look-up-down). The camera ends about where it
        started; next to an end it first turns away far enough for the gesture, and comes back
        after. Without a position yet it asks for one first (stop-ptz turns nothing when nothing
        turns)."""
        if self.position is None:
            self.stop()
        for direction, seconds in fit_gesture(GESTURES[name], self.position):
            self.move(direction, seconds)
        return self.position

    def patrol(self, out: str, tilt: Optional[float] = None, spread: float = PATROL_SPREAD) -> List[dict]:
        """Checks left, the middle and right of the room: spread degrees left of the middle of the pan
        range, the middle, and spread degrees right of it, saving a frame at each, then turns back
        to the middle. Anchors pan first when the session has not. The two ends of a wide pan range
        look at almost the same wall, so a patrol stays around the middle. Returns
        [{name, path, pan, tilt}]."""
        os.makedirs(out, exist_ok=True)
        if self.position is None:
            self.stop()
        if not self.position.pan_known:
            self.anchor(tilt=False)
        if tilt is not None:
            self.look_at(tilt=tilt)
        middle = self.position.pan_range / 2
        frames = []
        for name, angle in (("left", middle - spread), ("middle", middle), ("right", middle + spread)):
            self.glide_to(pan=angle)
            path = self.snapshot(os.path.join(out, "%s.jpg" % name))
            frames.append({"name": name, "path": path, "pan": self.position.pan, "tilt": self.position.tilt})
        self.glide_to(pan=middle)
        return frames

    def level(self, tilt: float) -> Position:
        """Comes back to a tilt you noted, such as eye level: runs to the bottom end, then glides up
        to tilt. Positions come from how long each turn ran, so after a long turn, a look to the
        sky above all, only a fresh anchor makes a noted tilt show the same view again."""
        self.to_end("DOWN")
        return self.glide("UP", tilt)

    def glide_to(self, pan: Optional[float] = None, tilt: Optional[float] = None) -> Position:
        """Turns to absolute angles with continuous moves: faster than look_at(), within about the
        same 15 degrees. The axes must be anchored."""
        for direction, degrees in turns_to(self.position, pan, tilt):
            self.glide(direction, degrees)
        return self.position

    def follow(self, seconds: float, out: str, settle: float = 0.4, frame=grey_frame) -> List[dict]:
        """Follows what moves for about seconds: takes two frames, finds where they differ, turns
        part of the way toward it, then looks again. Returns one entry per turn, {pan, tilt,
        turns}. Something has to move in view: a waving hand works, a still room turns nothing."""
        os.makedirs(out, exist_ok=True)
        first, second = os.path.join(out, "follow-a.jpg"), os.path.join(out, "follow-b.jpg")
        followed = []
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            before = frame(self.snapshot(first))
            after = frame(self.snapshot(second))
            center = motion_center(before, after, 64, 36)
            plan = follow_turns(center) if center else []
            for direction, degrees in plan:
                self.glide(direction, degrees)
            if plan:
                followed.append({"pan": self.position.pan, "tilt": self.position.tilt, "turns": plan})
                time.sleep(settle)
        return followed

    def checkpoint(self, name: str, **values) -> None:
        """Prints one JSON line for you to judge before going on."""
        if self.position is not None:
            values.setdefault("position", asdict(self.position))
        print(json.dumps(dict(checkpoint=name, **values)), file=self.log, flush=True)
