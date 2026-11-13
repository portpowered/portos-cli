"""Follow: turn toward what moves in view, such as a waving hand, for a number of seconds.

    python3 tricks/follow/run.py <camera> --seconds 30 --out follow/

Each round takes two frames, finds where they differ and turns part of the way toward it, so
the camera closes in over a few rounds without overshooting. A still room turns nothing. Prints
one checkpoint line, {"checkpoint": "follow", "turns": [{pan, tilt, turns}], ...}. Needs ffmpeg
on PATH. The hold is released only if this script opened it (PORTOS_LEASE unset).
"""

import argparse
import json
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "lib"))

from portos_camera import Camera, CameraError, Port  # noqa: E402


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("camera")
    parser.add_argument("--seconds", type=float, default=30.0, help="how long to follow")
    parser.add_argument("--out", default="follow", help="directory for the working frames")
    parser.add_argument("--cli", default="port", help="the port binary")
    parser.add_argument("--config-dir", default="", help="passed to port as --config-dir")
    args = parser.parse_args(argv)

    try:
        with Camera(args.camera, Port(args.cli, args.config_dir)) as camera:
            turns = camera.follow(args.seconds, args.out)
            camera.checkpoint("follow", turns=turns)
    except CameraError as failure:
        print(json.dumps({"trick": "follow", "ok": False, "error": str(failure)}), flush=True)
        return 1
    print(json.dumps({"trick": "follow", "ok": True}), flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
