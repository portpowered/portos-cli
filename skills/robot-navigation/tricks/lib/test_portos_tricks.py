"""Unit tests for portos_tricks: planning arithmetic and the correction loops, against a fake port."""

import io
import json
import math
import unittest

from portos_tricks import (
    Bounds, NotArrived, NotCarriedOut, HoldProblem, Pose, Robot, angle_diff, bearing, drive_plan, turn_plan,
)


class Planning(unittest.TestCase):
    def test_turn_plan_scales_omega_over_whole_seconds(self):
        self.assertEqual(turn_plan(73), [(0.637, 2000)])
        self.assertEqual(turn_plan(90), [(0.7854, 2000)])
        self.assertEqual(turn_plan(180), [(0.7854, 4000)])
        self.assertEqual(turn_plan(-45), [(-0.7854, 1000)])
        self.assertEqual(turn_plan(0), [])

    def test_turn_plan_splits_a_turn_longer_than_one_move(self):
        plan = turn_plan(360)
        self.assertEqual(len(plan), 2)
        for omega, duration in plan:
            self.assertLessEqual(abs(omega), Bounds().max_omega)
            self.assertLessEqual(duration, Bounds().max_duration_ms)
        self.assertAlmostEqual(sum(o * d / 1000 for o, d in plan), 2 * math.pi, places=3)

    def test_drive_plan_stays_inside_the_bounds(self):
        self.assertEqual(drive_plan(1.0), (0.25, 4000))
        self.assertEqual(drive_plan(0.1), (0.1, 1000))
        self.assertEqual(drive_plan(-0.3), (-0.3, 1000))
        self.assertEqual(drive_plan(9.0), (0.3, 5000))

    def test_angle_diff_takes_the_short_way(self):
        self.assertEqual(angle_diff(10, 350), 20)
        self.assertEqual(angle_diff(350, 10), -20)
        self.assertEqual(angle_diff(180, 0), 180)
        self.assertEqual(bearing(0, 0, 0, 1), 90)
        self.assertEqual(bearing(0, 0, -1, 0), 180)


class FakePort:
    """A robot on an open floor: moves follow the kinematics, scaled by `turn_gain` for turns, with
    an optional wall at x = wall_x that stops forward motion."""

    def __init__(self, x=0.0, y=0.0, heading=0.0, turn_gain=1.0, wall_x=None):
        self.x, self.y, self.heading = x, y, heading
        self.turn_gain = turn_gain
        self.wall_x = wall_x
        self.env = {}
        self.calls = []

    def _pose(self):
        return {"point": {"type": "Point", "coordinates": [self.x, self.y]}, "heading": self.heading,
                "relativeToGroupRouteId": "map:0", "observedAt": "2026-11-11T00:00:00Z"}

    def json(self, args):
        self.calls.append(list(args))
        if args[:2] == ["session", "hold"]:
            return {"holdId": "h1", "lease": "secret", "pose": None}
        if args[:2] == ["endpoint", "attribute"]:
            return {"value": self._pose()}
        if args[:2] == ["message", "remote-control/move"]:
            body = json.loads(args[args.index("--body") + 1])
            seconds = body["duration"] / 1000.0
            turned = math.degrees(body["omega"] * seconds) * self.turn_gain
            start = (self.x, self.y)
            self.heading = (self.heading + turned) % 360
            step = body["velocity"] * seconds
            nx = self.x + step * math.cos(math.radians(self.heading))
            if self.wall_x is not None and nx > self.wall_x:
                nx = self.wall_x
            self.y += (nx - self.x) * math.tan(math.radians(self.heading)) if abs(math.cos(math.radians(self.heading))) > 1e-9 else step
            self.x = nx
            moved = math.hypot(self.x - start[0], self.y - start[1])
            if body["velocity"] != 0 and moved < 0.05:
                raise NotCarriedOut(10, "the robot did not move as asked")
            return {"results": [{"message": {"body": {"pose": self._pose(), "distanceMetres": moved,
                                                       "turnedDegrees": turned}}}]}
        raise AssertionError("unexpected port call %s" % args)

    def run(self, args):
        self.calls.append(list(args))
        return 0, "{}", ""


class Correction(unittest.TestCase):
    def robot(self, port):
        return Robot("vacuum", port=port, log=io.StringIO())

    def test_turn_by_corrects_a_short_turn(self):
        port = FakePort(turn_gain=0.9)  # every turn delivers 90 percent
        robot = self.robot(port)
        robot.pose()
        turned = robot.turn_by(90)
        self.assertLess(abs(turned - 90), 5)
        moves = [c for c in port.calls if c[:2] == ["message", "remote-control/move"]]
        self.assertEqual(len(moves), 2, "one turn, then one correction for the missing 9 degrees")

    def test_turn_to_turns_the_short_way(self):
        port = FakePort(heading=350)
        robot = self.robot(port)
        robot.pose()
        robot.turn_to(20)
        body = json.loads(port.calls[-1][port.calls[-1].index("--body") + 1])
        self.assertGreater(body["omega"], 0, "350 to 20 is a 30 degree left turn")
        self.assertLess(abs(angle_diff(20, port.heading)), 5)

    def test_drive_to_reaches_the_point_in_legs_of_a_metre(self):
        port = FakePort()
        robot = self.robot(port)
        robot.pose()
        pose = robot.drive_to(2.5, 0.0)
        self.assertLess(math.hypot(pose.x - 2.5, pose.y), 0.10)
        legs = [json.loads(c[c.index("--body") + 1]) for c in port.calls if c[:2] == ["message", "remote-control/move"]]
        for leg in legs:
            self.assertLessEqual(abs(leg["velocity"] * leg["duration"] / 1000), 1.0 + 1e-9)
        self.assertEqual(len(robot.trail), 1 + len(legs))

    def test_drive_to_aims_before_driving(self):
        port = FakePort(heading=0)
        robot = self.robot(port)
        robot.pose()
        robot.drive_to(0.0, 1.0)
        first = json.loads(port.calls[1][port.calls[1].index("--body") + 1])
        self.assertEqual(first["velocity"], 0.0, "the first move turns toward the point")
        self.assertGreater(first["omega"], 0)

    def test_drive_to_reports_where_a_stall_stopped_it(self):
        port = FakePort(wall_x=0.5)
        robot = self.robot(port)
        robot.pose()
        with self.assertRaises(NotCarriedOut) as stalled:
            robot.drive_to(2.0, 0.0)
        self.assertAlmostEqual(stalled.exception.pose.x, 0.5)

    def test_drive_to_gives_up_after_its_moves(self):
        port = FakePort()
        robot = self.robot(port)
        robot.pose()
        with self.assertRaises(NotArrived):
            robot.drive_to(5.0, 0.0, max_moves=2)


class Holding(unittest.TestCase):
    def test_with_block_releases_the_hold_it_opened_even_on_failure(self):
        port = FakePort()
        with self.assertRaises(RuntimeError):
            with Robot("vacuum", port=port, log=io.StringIO()):
                self.assertEqual(port.env["PORTOS_LEASE"], "secret")
                raise RuntimeError("trick failed")
        self.assertIn(["session", "release", "h1", "--json"], port.calls)
        self.assertNotIn("PORTOS_LEASE", port.env)

    def test_an_existing_lease_is_used_and_left_open(self):
        port = FakePort()
        port.env["PORTOS_LEASE"] = "theirs"
        with Robot("vacuum", port=port, log=io.StringIO()):
            pass
        self.assertFalse([c for c in port.calls if c[:1] == ["session"]], "no hold opened or released")
        self.assertEqual(port.env["PORTOS_LEASE"], "theirs")

    def test_checkpoint_prints_one_json_line_with_the_pose(self):
        port = FakePort(x=1.0, y=2.0, heading=90)
        robot = Robot("vacuum", port=port, log=io.StringIO())
        robot.pose()
        out = io.StringIO()
        import contextlib
        with contextlib.redirect_stdout(out):
            robot.checkpoint("arrived", frames=["a.jpg"])
        record = json.loads(out.getvalue())
        self.assertEqual(record["checkpoint"], "arrived")
        self.assertEqual(record["frames"], ["a.jpg"])
        self.assertEqual(record["pose"]["x"], 1.0)

    def test_errors_map_from_exit_codes(self):
        from portos_tricks import _error
        failure = _error(13, json.dumps({"error": {"message": "hold_lease_required: no lease"}}), "")
        self.assertIsInstance(failure, HoldProblem)
        self.assertEqual(failure.detail, "hold_lease_required: no lease")


class Rendering(unittest.TestCase):
    def test_render_draws_rooms_holes_and_the_robot(self):
        from render_map import render, ROBOT, WHITE
        scene = {"rooms": [{"name": "Hall", "shape": {"type": "Polygon", "coordinates": [
            [[0, 0], [4, 0], [4, 4], [0, 4], [0, 0]], [[1, 1], [3, 1], [3, 3], [1, 3], [1, 1]]]}}],
            "robot": {"x": 0.5, "y": 0.5, "heading": 0}}
        canvas = render(scene, scale=10, margin=0)

        def colour(x, y):
            i = 3 * (int((4 - y) * 10) * canvas.width + int(x * 10))
            return tuple(canvas.pixels[i:i + 3])
        self.assertEqual((canvas.width, canvas.height), (41, 41))
        self.assertEqual(colour(0.5, 0.5), ROBOT)
        self.assertEqual(colour(1.55, 2.35), WHITE, "the hole is not filled")
        self.assertNotIn(colour(0.5, 3.5), (WHITE, ROBOT), "the room is filled")
        self.assertEqual(canvas.png()[:4], bytes([0x89]) + b"PNG")


if __name__ == "__main__":
    unittest.main()
