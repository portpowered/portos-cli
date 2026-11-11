"""Back and forth: drive between two points N times, by pose rather than by counting moves.

    python3 tricks/back-and-forth/run.py <endpoint> --laps 3 --distance 1.0

A is where the robot is (or --a x,y); B is --distance metres ahead (or --b x,y). Each lap drives
to B and back to A and prints one JSON line with how far each turnaround was from its point.
The last line is the result: {"trick": "back-and-forth", "ok": ..., "laps": [...]}. The exit
code is 0 only when every turnaround was within --tolerance.
"""

import argparse
import json
import math
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "lib"))

from portos_tricks import Port, Robot, RobotError, NotArrived  # noqa: E402


def point(text: str):
    x, y = (float(part) for part in text.split(","))
    return x, y


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("endpoint")
    parser.add_argument("--laps", type=int, default=2)
    parser.add_argument("--a", type=point, help="x,y of the first point (default: where the robot is)")
    parser.add_argument("--b", type=point, help="x,y of the second point")
    parser.add_argument("--distance", type=float, default=1.0, help="metres ahead to B when --b is not given")
    parser.add_argument("--tolerance", type=float, default=0.15, help="metres a turnaround may miss its point by")
    parser.add_argument("--cli", default="port", help="the port binary")
    parser.add_argument("--config-dir", default="", help="passed to port as --config-dir")
    args = parser.parse_args(argv)

    laps = []
    result = {"trick": "back-and-forth", "ok": False, "laps": laps}
    try:
        with Robot(args.endpoint, Port(args.cli, args.config_dir)) as robot:
            here = robot.pose()
            if here.heading is None:
                raise RobotError(11, "the robot reports no heading; is it still docked?")
            a = args.a or (here.x, here.y)
            b = args.b or (a[0] + args.distance * math.cos(math.radians(here.heading)),
                           a[1] + args.distance * math.sin(math.radians(here.heading)))
            result.update({"a": list(a), "b": list(b)})
            for lap in range(1, args.laps + 1):
                at_b = robot.drive_to(*b, tolerance=args.tolerance / 2)
                at_a = robot.drive_to(*a, tolerance=args.tolerance / 2)
                record = {
                    "lap": lap,
                    "missB": round(math.hypot(at_b.x - b[0], at_b.y - b[1]), 3),
                    "missA": round(math.hypot(at_a.x - a[0], at_a.y - a[1]), 3),
                }
                laps.append(record)
                print(json.dumps(record), flush=True)
            result["ok"] = all(max(lap["missA"], lap["missB"]) <= args.tolerance for lap in laps)
            result["trail"] = [[round(x, 3), round(y, 3)] for x, y in robot.trail]
    except (RobotError, NotArrived) as failure:
        result["error"] = str(failure)
        result["errorClass"] = type(failure).__name__
        pose = getattr(failure, "pose", None)
        if pose is not None:
            result["stoppedAt"] = [round(pose.x, 3), round(pose.y, 3)]
    print(json.dumps(result), flush=True)
    return 0 if result["ok"] else 1


if __name__ == "__main__":
    sys.exit(main())
