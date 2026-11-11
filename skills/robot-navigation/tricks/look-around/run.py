"""Look around: turn a full circle in steps, saving a frame at each, then hand back.

    python3 tricks/look-around/run.py <endpoint> --step 45 --out frames/

Prints one checkpoint line, {"checkpoint": "look-around", "frames": [{path, heading}], "pose": ...},
for you to look at the frames. The robot stays where it is; it docks only if this script opened
the hold (PORTOS_LEASE unset).
"""

import argparse
import json
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "lib"))

from portos_tricks import Port, Robot, RobotError  # noqa: E402


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("endpoint")
    parser.add_argument("--step", type=float, default=45.0, help="degrees between frames")
    parser.add_argument("--out", default="look-around", help="directory for the frames")
    parser.add_argument("--cli", default="port", help="the port binary")
    parser.add_argument("--config-dir", default="", help="passed to port as --config-dir")
    args = parser.parse_args(argv)

    try:
        with Robot(args.endpoint, Port(args.cli, args.config_dir)) as robot:
            robot.pose()
            frames = robot.look_around(args.step, args.out)
            robot.checkpoint("look-around", frames=frames)
    except RobotError as failure:
        print(json.dumps({"trick": "look-around", "ok": False, "error": str(failure)}), flush=True)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
