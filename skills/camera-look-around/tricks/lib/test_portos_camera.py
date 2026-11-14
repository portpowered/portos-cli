"""Unit tests for portos_camera: angle planning and the camera calls, against a fake port."""

import io
import itertools
import json
import os
import tempfile
import unittest

from portos_camera import (
    GESTURES, PATROL_SPREAD, Port, TO_END, TO_END_SECONDS, TURN_RATES, Camera, CameraError, HoldProblem, Position, fit_gesture,
    follow_turns, motion_center, scan_angles, turns_to,
)

PAN_RANGE, TILT_RANGE, STEP, RATE = 340.0, 169.0, 15.0, 20.0


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


class Gestures(unittest.TestCase):
    def test_every_gesture_ends_where_it_started_with_as_many_strokes_each_way(self):
        for name, strokes in GESTURES.items():
            net, count = {"PAN": 0.0, "TILT": 0.0}, {}
            for direction, seconds in strokes:
                axis = "TILT" if direction in ("UP", "DOWN") else "PAN"
                net[axis] += seconds if direction in ("RIGHT", "UP") else -seconds
                count[direction] = count.get(direction, 0) + 1
            self.assertEqual(net, {"PAN": 0.0, "TILT": 0.0}, name)
            self.assertEqual(count.get("LEFT", 0), count.get("RIGHT", 0), name)
            self.assertEqual(count.get("UP", 0), count.get("DOWN", 0), name)

    def test_every_gesture_stroke_is_long_enough_to_show(self):
        for name, strokes in GESTURES.items():
            for _, seconds in strokes:
                self.assertGreaterEqual(seconds, 0.2, name)

    def test_a_big_nod_at_the_bottom_end_turns_up_far_enough_first(self):
        nod = fit_gesture(GESTURES["big-nod"], known(170, 0))
        self.assertEqual(nod[0][0], "UP")
        self.assertAlmostEqual(nod[0][1], 0.8)
        self.assertEqual(nod[1:-1], GESTURES["big-nod"])

    def test_a_remembered_end_still_turns_a_gesture_away_from_it(self):
        at_bottom = Position(pan=170, tilt=0, pan_range=PAN_RANGE, tilt_range=TILT_RANGE, pan_known=False,
                             tilt_known=False, pan_remembered=True, tilt_remembered=True)
        self.assertEqual(fit_gesture(GESTURES["nod"], at_bottom), fit_gesture(GESTURES["nod"], known(170, 0)))

    def test_a_gesture_next_to_an_end_turns_away_first_and_back_after(self):
        nod = fit_gesture(GESTURES["nod"], known(170, 0))
        self.assertEqual(nod[0][0], "UP")
        self.assertAlmostEqual(nod[0][1], 0.4)
        self.assertEqual(nod[1:-1], GESTURES["nod"])
        self.assertEqual(nod[-1][0], "DOWN")
        jiggle = fit_gesture(GESTURES["jiggle"], known(PAN_RANGE - 2, 80))
        self.assertEqual(jiggle[0][0], "LEFT")
        self.assertAlmostEqual(jiggle[0][1], (0.25 * TURN_RATES["PAN"] - 2) / TURN_RATES["PAN"])
        self.assertEqual(fit_gesture(GESTURES["nod"], known(170, 80)), GESTURES["nod"])
        self.assertEqual(fit_gesture(GESTURES["nod"], None), GESTURES["nod"])


class Following(unittest.TestCase):
    WIDTH, HEIGHT = 8, 4

    def frame(self, lit=()):
        pixels = bytearray(self.WIDTH * self.HEIGHT)
        for x, y in lit:
            pixels[y * self.WIDTH + x] = 200
        return bytes(pixels)

    def test_motion_center_finds_the_changed_pixels(self):
        before, after = self.frame(), self.frame([(7, 0), (7, 1)])
        x, y = motion_center(before, after, self.WIDTH, self.HEIGHT)
        self.assertAlmostEqual(x, 0.5)
        self.assertLess(y, 0)

    def test_motion_center_ignores_a_still_frame_and_small_noise(self):
        self.assertIsNone(motion_center(self.frame(), self.frame(), self.WIDTH, self.HEIGHT))
        self.assertIsNone(motion_center(self.frame(), self.frame([(3, 3)]), self.WIDTH, self.HEIGHT, least=0.1))

    def test_follow_turns_toward_the_motion_by_part_of_the_offset(self):
        self.assertEqual(follow_turns((0.5, -0.25), view=(100, 60), gain=0.5), [("RIGHT", 25), ("UP", 8)])
        self.assertEqual(follow_turns((-0.3, 0.05), view=(100, 60), gain=1), [("LEFT", 30)])
        self.assertEqual(follow_turns((0.05, 0.05)), [])


class JSONErrorTest(unittest.TestCase):
    def test_a_failed_command_raises_the_message_from_its_json_error_body(self):
        port = Port()
        body = json.dumps({"error": {"message": "the hold ended (hold_ended)", "type": "cli_error"}}, indent=2)
        port.run = lambda args: (13, "", body)
        with self.assertRaises(HoldProblem) as raised:
            port.json(["message", "stop-ptz", "cam"])
        self.assertEqual(raised.exception.detail, "the hold ended (hold_ended)")

    def test_a_failed_command_without_json_raises_its_last_line(self):
        port = Port()
        port.run = lambda args: (1, "", "starting\nport: no such endpoint\n")
        with self.assertRaises(CameraError) as raised:
            port.json(["message", "stop-ptz", "cam"])
        self.assertEqual(raised.exception.detail, "port: no such endpoint")


class FakePort:
    """A pan-tilt camera that turns in whole steps, stops at its ends and reports ptz-position as
    the CLI prints it with --json."""

    def __init__(self, pan=150.0, tilt=60.0):
        self.pan, self.tilt = pan, tilt  # true angles from the left and bottom ends
        self.start = (pan, tilt)
        self.pan_known = self.tilt_known = False
        # remembered reports both axes from the ends as the last session left them.
        self.remembered = False
        self.env = {}
        self.calls = []
        self.fail = None
        self.rate = RATE

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
            degrees = float(args[args.index("--degrees") + 1]) if "--degrees" in args else 0
            return self._turn(args[3], max(1, round(degrees / STEP)) * STEP if degrees else STEP, degrees >= TO_END)
        if args[:2] == ["message", "move-ptz"]:
            if "--seconds" not in args:
                return self._report(0, moving=args[3])
            seconds = float(args[args.index("--seconds") + 1])
            speed = float(args[args.index("--speed") + 1]) if "--speed" in args else 1.0
            return self._turn(args[3], seconds * self.rate * speed, seconds >= TO_END_SECONDS)
        if args[:2] == ["message", "stop-ptz"]:
            return self._report(0)
        raise AssertionError("unexpected command %s" % args)

    def _turn(self, direction, degrees, to_end):
        sign = 1 if direction in ("RIGHT", "UP") else -1
        axis, span = ("pan", PAN_RANGE) if direction in ("LEFT", "RIGHT") else ("tilt", TILT_RANGE)
        before = getattr(self, axis)
        after = min(max(before + sign * degrees, 0), span)
        setattr(self, axis, after)
        if to_end:
            setattr(self, axis + "_known", True)
        return self._report(abs(after - before))

    def _report(self, moved, moving=None):
        def reported(name, value, start):
            return value if getattr(self, name + "_known") or self.remembered else value - start

        def reference(name, end):
            if getattr(self, name + "_known"):
                return end
            return "REMEMBERED_" + end if self.remembered else "SESSION_START"
        return {"position": {
            "movedDegrees": moved,
            "panMoving": moving in ("LEFT", "RIGHT"), "tiltMoving": moving in ("UP", "DOWN"),
            "panDegrees": reported("pan", self.pan, self.start[0]), "panRangeDegrees": PAN_RANGE,
            "panReference": reference("pan", "LEFT_END"),
            "tiltDegrees": reported("tilt", self.tilt, self.start[1]), "tiltRangeDegrees": TILT_RANGE,
            "tiltReference": reference("tilt", "BOTTOM_END"),
        }}


class CameraCalls(unittest.TestCase):
    def setUp(self):
        self.port = FakePort()
        self.log = io.StringIO()

    def test_a_remembered_position_is_not_known_until_trusted(self):
        self.port.remembered = True
        camera = Camera("cam", self.port, self.log)
        position = camera.where()
        self.assertTrue(position.pan_remembered and position.tilt_remembered)
        self.assertFalse(position.pan_known or position.tilt_known)
        self.assertEqual((position.pan, position.tilt), (150.0, 60.0))
        camera.look_at(pan=200)
        self.assertIn(["message", "move-ptz", "cam", "LEFT", "--seconds", "%g" % TO_END_SECONDS], self.port.calls)

    def test_trusted_remembered_angles_turn_without_anchoring(self):
        self.port.remembered = True
        camera = Camera("cam", self.port, self.log)
        self.assertTrue(camera.trust_remembered().pan_known)
        self.port.calls.clear()
        camera.look_at(pan=210)
        self.assertEqual(self.port.calls, [["message", "step-ptz", "cam", "RIGHT", "--degrees", "60"]])
        self.assertTrue(camera.position.pan_known)

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
        self.assertEqual(self.port.calls[0][:2], ["message", "move-ptz"])
        self.assertEqual(turns[:2], [["LEFT", "--seconds", "60"], ["DOWN", "--seconds", "60"]])
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

    def test_move_turns_for_a_time_or_until_stopped(self):
        camera = Camera("cam", self.port, self.log)
        camera.anchor()
        self.port.calls.clear()
        nudged = camera.move("RIGHT", 0.5)
        self.assertEqual(self.port.calls[0], ["message", "move-ptz", "cam", "RIGHT", "--seconds", "0.5"])
        self.assertEqual(nudged.pan, 10)
        self.assertFalse(nudged.pan_moving)
        turning = camera.move("UP", speed=0.3)
        self.assertEqual(self.port.calls[1], ["message", "move-ptz", "cam", "UP", "--speed", "0.3"])
        self.assertTrue(turning.tilt_moving)
        camera.stop("TILT")
        camera.stop()
        self.assertEqual(self.port.calls[2:], [["message", "stop-ptz", "cam", "--axis", "TILT"], ["message", "stop-ptz", "cam"]])
        self.assertFalse(camera.position.tilt_moving)

    def test_move_clamps_seconds_and_speed_and_to_end_runs_the_longest_move(self):
        camera = Camera("cam", self.port, self.log)
        camera.move("LEFT", 0.01, 5)
        camera.move("LEFT", 600)
        camera.to_end("RIGHT")
        self.assertEqual([call[4:] for call in self.port.calls], [
            ["--seconds", "0.1", "--speed", "1"], ["--seconds", "%g" % TO_END_SECONDS], ["--seconds", "%g" % TO_END_SECONDS]])
        self.assertEqual(camera.position.pan, PAN_RANGE)
        self.assertTrue(camera.position.pan_known)

    def test_glide_turns_by_angle_in_one_move(self):
        camera = Camera("cam", self.port, self.log)
        camera.anchor()
        self.port.calls.clear()
        camera.glide("RIGHT", 60)
        camera.glide("UP", 0.4)
        self.assertEqual(self.port.calls, [["message", "move-ptz", "cam", "RIGHT", "--seconds", "%g" % (60 / TURN_RATES["PAN"])]])

    def test_gesture_plays_its_strokes_and_comes_back(self):
        camera = Camera("cam", self.port, self.log)
        camera.anchor()
        camera.glide("UP", 80)
        start = (self.port.pan, self.port.tilt)
        self.port.calls.clear()
        camera.gesture("nod")
        self.assertEqual([call[3:] for call in self.port.calls],
                         [[direction, "--seconds", "%g" % seconds] for direction, seconds in GESTURES["nod"]])
        self.assertEqual((self.port.pan, self.port.tilt), start)

    def test_gesture_reads_the_position_first_and_makes_room_at_an_end(self):
        self.port.tilt, self.port.tilt_known = 0.0, True
        Camera("cam", self.port, self.log).gesture("nod")
        self.assertEqual(self.port.calls[0], ["message", "stop-ptz", "cam"])
        self.assertEqual(self.port.calls[1][3], "UP")
        self.assertEqual(len(self.port.calls), 1 + len(GESTURES["nod"]) + 2)
        self.assertAlmostEqual(self.port.tilt, 0.0)

    def test_patrol_reads_the_position_and_keeps_an_anchored_pan(self):
        self.port.rate = TURN_RATES["PAN"]
        self.port.pan, self.port.pan_known = PAN_RANGE / 2, True
        with tempfile.TemporaryDirectory() as out:
            Camera("cam", self.port, self.log).patrol(out)
        self.assertEqual(self.port.calls[0], ["message", "stop-ptz", "cam"])
        self.assertNotIn(str(TO_END_SECONDS), [call[-1] for call in self.port.calls])

    def test_level_runs_to_the_bottom_then_glides_up(self):
        self.port.rate = TURN_RATES["TILT"]
        camera = Camera("cam", self.port, self.log)
        position = camera.level(88)
        moves = [call[3] for call in self.port.calls if call[:2] == ["message", "move-ptz"]]
        self.assertEqual(moves, ["DOWN", "UP"])
        self.assertTrue(position.tilt_known)
        self.assertAlmostEqual(self.port.tilt, 88, places=2)

    def test_patrol_checks_left_middle_and_right_around_the_middle(self):
        self.port.rate = TURN_RATES["PAN"]
        camera = Camera("cam", self.port, self.log)
        with tempfile.TemporaryDirectory() as out:
            frames = camera.patrol(out)
            self.assertEqual([frame["name"] for frame in frames], ["left", "middle", "right"])
            middle = PAN_RANGE / 2
            for frame, pan in zip(frames, (middle - PATROL_SPREAD, middle, middle + PATROL_SPREAD)):
                self.assertAlmostEqual(frame["pan"], pan, places=2)
            for frame in frames:
                self.assertTrue(os.path.exists(frame["path"]))
        self.assertAlmostEqual(self.port.pan, PAN_RANGE / 2, places=2)

    def test_follow_turns_toward_what_moved_until_the_time_is_up(self):
        camera = Camera("cam", self.port, self.log)
        camera.anchor()
        frames = itertools.chain([bytes(64 * 36), bytes([0] * 63 + [200]) * 36], itertools.repeat(bytes(64 * 36)))
        with tempfile.TemporaryDirectory() as out:
            followed = camera.follow(0.2, out, settle=0, frame=lambda path: next(frames))
        self.assertEqual(followed, [{"pan": self.port.pan, "tilt": 0.0, "turns": [("RIGHT", round(0.5 * 130 * 0.6))]}])

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
