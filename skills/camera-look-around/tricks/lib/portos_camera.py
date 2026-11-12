"""Starter helper for camera tricks: a small layer over the `port` CLI for a pan-tilt camera.

Copy it next to your tricks and improve it. It needs Python 3.8+ and nothing outside the
standard library. Every camera command goes through `port`.

    from portos_camera import Camera

    with Camera("ring-camera") as camera:   # shared hold; leaving the block releases it
        camera.anchor()                     # left and bottom ends, so angles are exact
        camera.look_at(pan=170, tilt=40)
        camera.snapshot("sofa.jpg")

If PORTOS_LEASE is already set (you ran `port session hold` yourself), Camera uses that hold and
leaves it open when the block ends: whoever opens a hold releases it.

Angles: pan counts degrees right of the left end and tilt counts degrees up from the bottom end,
once the camera has been turned to that end in this camera session (`anchor()` does both). Before
that they count from where the session started, and `pan_known` / `tilt_known` are False.
"""

import json
import os
import subprocess
import sys
from dataclasses import dataclass, asdict
from typing import List, Optional, Sequence, Tuple

# Exit classes of the `port` CLI.
EXIT_INVALID = 2
EXIT_BUSY = 12
EXIT_HOLD = 13
EXIT_CAMERA = 14

# A turn of 360 degrees runs the camera to the end of its range in that direction.
TO_END = 360


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
            lines = [line for line in (err or out).strip().splitlines() if line.strip()]
            raise _ERRORS.get(code, CameraError)(code, lines[-1] if lines else "port exited %d" % code)
        return json.loads(out) if out.strip() else {}


class Camera:
    """One held pan-tilt camera. Use it in a with block so a hold it opened is released."""

    def __init__(self, endpoint: str, port: Optional[Port] = None, log=None):
        self.endpoint = endpoint
        self.port = port or Port()
        self.log = log or sys.stdout
        self.hold_id = ""
        self.opened = False
        self.position: Optional[Position] = None

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
        reply = self.port.json(args)
        if not reply.get("position"):
            raise CameraError(0, "the camera reported no position after the turn")
        self.position = Position.from_reply(reply["position"])
        return self.position

    def anchor(self, pan: bool = True, tilt: bool = True) -> Position:
        """Turns to the left end and the bottom end, so later angles are exact."""
        if pan:
            self.turn("LEFT", TO_END)
        if tilt:
            self.turn("DOWN", TO_END)
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

    def checkpoint(self, name: str, **values) -> None:
        """Prints one JSON line for you to judge before going on."""
        if self.position is not None:
            values.setdefault("position", asdict(self.position))
        print(json.dumps(dict(checkpoint=name, **values)), file=self.log, flush=True)
