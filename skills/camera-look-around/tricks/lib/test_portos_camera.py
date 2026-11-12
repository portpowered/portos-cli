"""Unit tests for portos_camera: angle planning and the camera calls, against a fake port."""

import io
import json
import os
import tempfile
import unittest

from portos_camera import (
    TO_END, Camera, CameraError, HoldProblem, Position, scan_angles, turns_to,
)

PAN_RANGE, TILT_RANGE, STEP = 340.0, 169.0, 15.0


def known(pan, tilt):
    return Position(pan=pan, tilt=tilt, pan_range=PAN_RANGE, tilt_range=TILT_RANGE, pan_known=True, tilt_known=True)


class Planning(unittest.TestCase):
    def test_turns_to_takes_each_axis_the_right_way(self):
        self.assertEqual(turns_to(known(100, 20), pan=40, tilt=80), [("LEFT", 60), ("UP", 60)])
        self.assertEqual(turns_to(known(0, 80), pan=170), [("RIGHT", 170)])
        self.assertEqual(turns_to(known(0, 80), tilt=10), [("DOWN", 70)])

    def test_turns_to_clamps_to_the_range_and_skips_small_differences(self):
        self.assertEqual(turns_to(known(300, 0), pan=500), [("RIGHT", 40)])
        self.assertEqual(turns_to(known(100, 50), pan=103, tilt=-20), [("DOWN", 50)])

    def test_turns_to_refuses_an_axis_counted_from_the_session_start(self):
        start = Position(pan=-15, tilt=0, pan_range=PAN_RANGE, tilt_range=TILT_RANGE, pan_known=False, tilt_known=True)
        with self.assertRaises(ValueError):
            turns_to(start, pan=100)
        self.assertEqual(turns_to(start, tilt=30), [("UP", 30)])

    def test_scan_angles_cover_the_range_evenly(self):
        self.assertEqual(scan_angles(340, 60), [0, 57, 113, 170, 227, 283, 340])
        self.assertEqual(scan_angles(120, 60), [0, 60, 120])
        self.assertEqual(scan_angles(0, 60), [0.0])


class FakePort:
    """A pan-tilt camera that turns in whole steps, stops at its ends and reports ptz-position as
    the CLI prints it with --json."""

    def __init__(self, pan=150.0, tilt=60.0):
        self.pan, self.tilt = pan, tilt  # true angles from the left and bottom ends
        self.start = (pan, tilt)
        self.pan_known = self.tilt_known = False
        self.env = {}
        self.calls = []
        self.fail = None

    def json(self, args):
        self.calls.append(list(args))
        if self.fail:
            raise self.fail
        if args[:2] == ["session", "hold"]:
            return {"holdId": "hold-1", "lease": "lease-1"}
        if args[:2] == ["session", "release"]:
            return {}
        if args[:2] == ["message", "snapshot"]:
            open(args[args.index("--output") + 1], "wb").close()
            return {}
        if args[:2] == ["message", "step-ptz"]:
            return self._turn(args[3], float(args[args.index("--degrees") + 1]) if "--degrees" in args else 0)
        raise AssertionError("unexpected command %s" % args)

    def _turn(self, direction, degrees):
        steps = max(1, round(degrees / STEP)) if degrees else 1
        sign = 1 if direction in ("RIGHT", "UP") else -1
        axis, span = ("pan", PAN_RANGE) if direction in ("LEFT", "RIGHT") else ("tilt", TILT_RANGE)
        before = getattr(self, axis)
        after = min(max(before + sign * steps * STEP, 0), span)
        setattr(self, axis, after)
        if degrees >= TO_END:
            setattr(self, axis + "_known", True)
        def reported(name, value, start):
            return value if getattr(self, name + "_known") else value - start
        return {"position": {
            "movedDegrees": abs(after - before),
            "panDegrees": reported("pan", self.pan, self.start[0]), "panRangeDegrees": PAN_RANGE,
            "panReference": "LEFT_END" if self.pan_known else "SESSION_START",
            "tiltDegrees": reported("tilt", self.tilt, self.start[1]), "tiltRangeDegrees": TILT_RANGE,
            "tiltReference": "BOTTOM_END" if self.tilt_known else "SESSION_START",
        }}


class CameraCalls(unittest.TestCase):
    def setUp(self):
        self.port = FakePort()
        self.log = io.StringIO()

    def test_hold_opens_a_shared_hold_and_releases_it(self):
        with Camera("cam", self.port, self.log) as camera:
            self.assertEqual(self.port.env["PORTOS_LEASE"], "lease-1")
            camera.turn("LEFT")
        self.assertEqual(self.port.calls[0], ["session", "hold", "cam", "--camera", "--shared"])
        self.assertEqual(self.port.calls[-1], ["session", "release", "hold-1"])
        self.assertNotIn("PORTOS_LEASE", self.port.env)

    def test_an_existing_lease_is_used_and_left_open(self):
        self.port.env["PORTOS_LEASE"] = "mine"
        with Camera("cam", self.port, self.log) as camera:
            camera.turn("UP", 30)
        self.assertEqual(self.port.calls, [["message", "step-ptz", "cam", "UP", "--degrees", "30"]])
        self.assertEqual(self.port.env["PORTOS_LEASE"], "mine")

    def test_look_at_anchors_unknown_axes_then_turns_to_the_angles(self):
        camera = Camera("cam", self.port, self.log)
        position = camera.look_at(pan=170, tilt=45)
        turns = [call[3:] for call in self.port.calls]
        self.assertEqual(turns[:2], [["LEFT", "--degrees", "360"], ["DOWN", "--degrees", "360"]])
        self.assertTrue(position.pan_known and position.tilt_known)
        self.assertAlmostEqual(position.pan, 165, delta=STEP)
        self.assertAlmostEqual(position.tilt, 45, delta=STEP)

    def test_look_at_on_a_known_axis_turns_without_anchoring_again(self):
        camera = Camera("cam", self.port, self.log)
        first = camera.look_at(pan=100)
        self.port.calls.clear()
        camera.look_at(pan=40)
        degrees = "%g" % round(first.pan - 40)
        self.assertEqual(self.port.calls, [["message", "step-ptz", "cam", "LEFT", "--degrees", degrees]])

    def test_panorama_sweeps_the_full_range_left_to_right(self):
        camera = Camera("cam", self.port, self.log)
        with tempfile.TemporaryDirectory() as out:
            frames = camera.panorama(out, every=90)
            self.assertEqual(len(frames), 5)
            self.assertEqual(frames[0]["pan"], 0)
            self.assertEqual(frames[-1]["pan"], PAN_RANGE)
            pans = [frame["pan"] for frame in frames]
            self.assertEqual(pans, sorted(pans))
            for frame in frames:
                self.assertTrue(os.path.exists(frame["path"]))

    def test_a_refused_hold_raises_its_typed_error(self):
        self.port.fail = HoldProblem(13, "hold_ended")
        with self.assertRaises(HoldProblem):
            Camera("cam", self.port, self.log).turn("LEFT")

    def test_a_turn_without_a_position_reply_is_an_error(self):
        self.port.json = lambda args: {}
        with self.assertRaises(CameraError):
            Camera("cam", self.port, self.log).turn("LEFT")

    def test_checkpoint_prints_the_position(self):
        camera = Camera("cam", self.port, self.log)
        camera.turn("RIGHT")
        camera.checkpoint("looked", note="window")
        line = json.loads(self.log.getvalue())
        self.assertEqual(line["checkpoint"], "looked")
        self.assertEqual(line["note"], "window")
        self.assertIn("pan", line["position"])


if __name__ == "__main__":
    unittest.main()
